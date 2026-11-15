package gb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestAudioOfferValidation(t *testing.T) {
	for _, test := range []struct {
		body  string
		valid bool
	}{
		{"m=audio 12000 TCP/RTP/AVP 8\r\na=recvonly\r\na=setup:active\r\ny=0200000001\r\n", true},
		{"m=audio 12000 RTP/AVP 8\r\na=recvonly\r\n", true},
		{"m=audio 12000 TCP/RTP/AVP 0\r\n", false},
		{"m=audio 0 RTP/AVP 8\r\n", false},
		{"m=audio 65536 RTP/AVP 8\r\n", false},
		{"m=audio 12000 RTP/AVP 8\r\na=sendonly\r\n", false},
		{"m=video 12000 RTP/AVP 8\r\n", false},
		{"m=audio 12000 UNKNOWN 8\r\n", false},
		{"m=audio 12000 RTP/AVP 8\r\nm=audio 13000 RTP/AVP 8\r\n", false},
	} {
		_, err := parseAudioOffer(test.body)
		if (err == nil) != test.valid {
			t.Errorf("%q: %v", test.body, err)
		}
	}
}

// Exercise the actual Broadcast -> camera INVITE -> SDP -> ACK -> TCP/RTP
// path rather than just the parser. No physical microphone or camera is used.
func TestBroadcastTCPHandshakeAndAudio(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "talk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const device = "34020000001320000001"
	const server = "34020000002000000002"
	if err = store.SeedDevice(device, "fixture", "camera", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	if err = store.saveChannels(device, []Channel{{ID: device, Name: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	platform, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer platform.Close()
	port := platform.LocalAddr().(*net.UDPAddr).Port
	g, err := New(Config{PublicIP: "127.0.0.1", SIPPort: port, ServerID: server, Realm: "3402000000"}, store)
	if err != nil {
		t.Fatal(err)
	}
	defer g.ua.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	go g.srv.ServeUDP(platform)
	cam, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer cam.Close()
	_ = cam.SetDeadline(time.Now().Add(5 * time.Second))
	g.online[device] = registration{addr: cam.LocalAddr().(*net.UDPAddr).AddrPort(), expires: time.Now().Add(time.Hour), seen: time.Now()}
	// The source address must be the same camera that registered.
	if _, err = g.BeginTalk(ctx, DefaultSpaceID+1, device, device, 31000, 31100); err == nil {
		t.Fatal("foreign space started audio")
	}
	packets := make(chan []byte, 1)
	failures := make(chan error, 1)
	go func() {
		fail := func(err error) { failures <- err }
		buffer := make([]byte, 16384)
		n, remote, e := cam.ReadFromUDP(buffer)
		if e != nil {
			fail(e)
			return
		}
		request := string(buffer[:n])
		if !strings.Contains(request, "<CmdType>Broadcast</CmdType>") {
			fail(fmt.Errorf("missing Broadcast"))
			return
		}
		header := func(message, name string) string {
			for _, line := range strings.Split(message, "\r\n") {
				if strings.HasPrefix(strings.ToLower(line), strings.ToLower(name)+":") {
					return strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
				}
			}
			return ""
		}
		var response strings.Builder
		response.WriteString("SIP/2.0 200 OK\r\n")
		for _, name := range []string{"Via", "From", "To", "Call-ID", "CSeq"} {
			fmt.Fprintf(&response, "%s: %s\r\n", name, header(request, name))
		}
		response.WriteString("Content-Length: 0\r\n\r\n")
		_, _ = cam.WriteToUDP([]byte(response.String()), remote)
		sdp := "v=0\r\no=" + device + " 0 0 IN IP4 127.0.0.1\r\ns=Play\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 12000 TCP/RTP/AVP 8\r\na=recvonly\r\na=setup:active\r\ny=0200000001\r\n"
		common := fmt.Sprintf("Via: SIP/2.0/UDP %s;branch=z9hG4bK-fixture-invite\r\nFrom: <sip:%s@3402000000>;tag=fixture\r\nTo: <sip:%s@3402000000>\r\nCall-ID: fixture-talk\r\n", cam.LocalAddr(), device, server)
		invite := fmt.Sprintf("INVITE sip:%s@127.0.0.1:%d SIP/2.0\r\n%sCSeq: 1 INVITE\r\nContact: <sip:%s@%s>\r\nMax-Forwards: 70\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s", server, port, common, device, cam.LocalAddr(), len(sdp), sdp)
		_, _ = cam.WriteToUDP([]byte(invite), remote)
		for {
			n, _, e = cam.ReadFromUDP(buffer)
			if e != nil {
				fail(e)
				return
			}
			response = strings.Builder{}
			request = string(buffer[:n])
			if strings.HasPrefix(request, "SIP/2.0 200") {
				break
			}
		}
		mediaPort := 0
		for _, line := range strings.Split(request, "\r\n") {
			if strings.HasPrefix(line, "m=audio ") {
				mediaPort, _ = strconv.Atoi(strings.Fields(line)[1])
			}
		}
		if mediaPort == 0 {
			fail(fmt.Errorf("no audio port"))
			return
		}
		ack := fmt.Sprintf("ACK sip:%s@127.0.0.1:%d SIP/2.0\r\nVia: SIP/2.0/UDP %s;branch=z9hG4bK-fixture-ack\r\nFrom: %s\r\nTo: %s\r\nCall-ID: fixture-talk\r\nCSeq: 1 ACK\r\nMax-Forwards: 70\r\nContent-Length: 0\r\n\r\n", server, port, cam.LocalAddr(), header(request, "From"), header(request, "To"))
		_, _ = cam.WriteToUDP([]byte(ack), remote)
		conn, e := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", mediaPort), time.Second)
		if e != nil {
			fail(e)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var length [2]byte
		if _, e = io.ReadFull(conn, length[:]); e != nil {
			fail(e)
			return
		}
		packet := make([]byte, binary.BigEndian.Uint16(length[:]))
		if _, e = io.ReadFull(conn, packet); e != nil {
			fail(e)
			return
		}
		packets <- packet
	}()
	call, err := g.BeginTalk(ctx, DefaultSpaceID, device, device, 31000, 31100)
	if err != nil {
		select {
		case e := <-failures:
			t.Fatalf("camera: %v; begin: %v", e, err)
		default:
			t.Fatal(err)
		}
	}
	defer call.Close()
	if _, err = g.BeginTalk(ctx, DefaultSpaceID, device, device, 31000, 31100); err != ErrTalkBusy {
		t.Fatalf("second talk: %v", err)
	}
	payload := bytes.Repeat([]byte{0xd5}, 160)
	if err = call.WriteAudio(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-packets:
		var packet rtp.Packet
		if err = packet.Unmarshal(raw); err != nil || packet.PayloadType != 8 || packet.SSRC != 200000001 || !bytes.Equal(packet.Payload, payload) {
			t.Fatalf("audio mismatch: %+v %v", packet, err)
		}
	case err := <-failures:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("audio timeout")
	}
	call.Close()
	if err = call.WriteAudio(payload); err == nil {
		t.Fatal("closed call accepted audio")
	}
	g.mu.Lock()
	remaining := len(g.talks)
	g.mu.Unlock()
	if remaining != 0 {
		t.Fatal("call not released")
	}
}
