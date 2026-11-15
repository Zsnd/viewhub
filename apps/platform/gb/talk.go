package gb

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/pion/rtp"
)

var ErrTalkBusy = errors.New("device talk is busy")

// Talk carries G.711 A-law speech after the camera accepts a Broadcast request.
// Device audio is received independently through its live video stream.
type Talk struct {
	g               *Gateway
	device, channel string
	source          netip.Addr
	signaling       netip.AddrPort
	tcp             *net.TCPListener
	udp             *net.UDPConn
	port            int
	Ready           chan struct{}
	Done            chan struct{}
	once            sync.Once
	mu              sync.Mutex
	dialog          *sipgo.DialogServerSession
	transaction     sip.ServerTransaction
	conn            net.Conn
	remote          *net.UDPAddr
	sequence        uint16
	timestamp, ssrc uint32
}

func (g *Gateway) BeginTalk(ctx context.Context, space int64, device, channel string, first, last int) (*Talk, error) {
	if !g.Store.HasChannel(space, device, channel) {
		return nil, errors.New("unknown channel")
	}
	t := &Talk{g: g, device: device, channel: channel, Ready: make(chan struct{}), Done: make(chan struct{})}
	for port := first; port <= last; port++ {
		listener, err := net.ListenTCP("tcp4", &net.TCPAddr{Port: port})
		if err != nil {
			continue
		}
		udp, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if err != nil {
			listener.Close()
			continue
		}
		t.tcp, t.udp, t.port = listener, udp, port
		break
	}
	if t.tcp == nil {
		return nil, errors.New("no audio port available")
	}
	var seed [10]byte
	if _, err := rand.Read(seed[:]); err != nil {
		t.tcp.Close()
		t.udp.Close()
		return nil, err
	}
	t.sequence = binary.BigEndian.Uint16(seed[:2])
	t.timestamp = binary.BigEndian.Uint32(seed[2:6])
	t.ssrc = binary.BigEndian.Uint32(seed[6:])
	g.mu.Lock()
	r, ok := g.online[device]
	if !ok || time.Now().After(r.expires) || !g.Store.HasChannel(space, device, channel) {
		g.mu.Unlock()
		t.tcp.Close()
		t.udp.Close()
		return nil, errors.New("device offline")
	}
	if g.talks[device] != nil {
		g.mu.Unlock()
		t.tcp.Close()
		t.udp.Close()
		return nil, ErrTalkBusy
	}
	t.source = r.addr.Addr()
	t.signaling = r.addr
	g.talks[device] = t
	g.mu.Unlock()
	body := fmt.Sprintf("<Notify><CmdType>Broadcast</CmdType><SN>%d</SN><SourceID>%s</SourceID><TargetID>%s</TargetID></Notify>", g.serial.Add(1), g.cfg.ServerID, channel)
	if err := g.sendXML(ctx, device, body); err != nil {
		t.Close()
		return nil, err
	}
	select {
	case <-t.Ready:
		return t, nil
	case <-t.Done:
		return nil, errors.New("device rejected audio")
	case <-ctx.Done():
		t.Close()
		return nil, ctx.Err()
	}
}

type audioOffer struct {
	tcp, passive bool
	port         int
	ssrc         uint32
}

func parseAudioOffer(body string) (audioOffer, error) {
	var o audioOffer
	if len(body) > 16384 {
		return o, errors.New("SDP too large")
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r", ""), "\n")
	inAudio := false
	found, pcma := false, false
	for _, line := range lines {
		if strings.HasPrefix(line, "m=") {
			f := strings.Fields(strings.TrimPrefix(line, "m="))
			inAudio = len(f) >= 4 && f[0] == "audio"
			if !inAudio {
				continue
			}
			if found {
				return o, errors.New("multiple audio sections")
			}
			found = true
			n, err := strconv.Atoi(f[1])
			if err != nil || n < 1 || n > 65535 {
				return o, errors.New("invalid port")
			}
			o.port = n
			if f[2] != "RTP/AVP" && f[2] != "TCP/RTP/AVP" {
				return o, errors.New("unsupported transport")
			}
			o.tcp = f[2] == "TCP/RTP/AVP"
			for _, pt := range f[3:] {
				if pt == "8" {
					pcma = true
				}
			}
		}
		if inAudio && line == "a=setup:passive" {
			o.passive = true
		}
		if inAudio && line == "a=sendonly" {
			return o, errors.New("camera cannot receive audio")
		}
		if inAudio && strings.HasPrefix(line, "a=rtpmap:8 ") && !strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "a=rtpmap:8 ")), "PCMA/8000") && !strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "a=rtpmap:8 ")), "PCMA/8000/1") {
			return o, errors.New("invalid PCMA mapping")
		}
		if strings.HasPrefix(line, "y=") {
			n, err := strconv.ParseUint(strings.TrimPrefix(line, "y="), 10, 32)
			if err == nil {
				o.ssrc = uint32(n)
			}
		}
	}
	if !found || !pcma {
		return o, errors.New("PCMA is required")
	}
	return o, nil
}

func (g *Gateway) talkInvite(req *sip.Request, tx sip.ServerTransaction) {
	source, err := netip.ParseAddrPort(req.Source())
	if err != nil || req.From() == nil || req.Recipient.User != g.cfg.ServerID {
		respond(tx, req, 403, "Forbidden")
		return
	}
	g.mu.Lock()
	var t *Talk
	for _, candidate := range g.talks {
		if candidate.source == source.Addr() && (strings.EqualFold(req.Transport(), "TCP") || candidate.signaling == source) && (req.From().Address.User == candidate.device || req.From().Address.User == candidate.channel) {
			t = candidate
			break
		}
	}
	g.mu.Unlock()
	if t == nil {
		respond(tx, req, 403, "Unsolicited Audio")
		return
	}
	offer, err := parseAudioOffer(string(req.Body()))
	if err != nil {
		respond(tx, req, 488, "Unsupported Audio")
		t.Close()
		return
	}
	t.mu.Lock()
	if t.dialog != nil {
		t.mu.Unlock()
		respond(tx, req, 486, "Busy Here")
		return
	}
	select {
	case <-t.Done:
		t.mu.Unlock()
		respond(tx, req, 481, "Audio Ended")
		return
	default:
	}
	dialog, err := g.dialogs.ReadInvite(req, tx)
	if err != nil {
		t.mu.Unlock()
		respond(tx, req, 400, "Invalid Invite")
		t.Close()
		return
	}
	t.dialog = dialog
	t.transaction = tx
	if offer.ssrc != 0 {
		t.ssrc = offer.ssrc
	}
	t.mu.Unlock()
	transport, setup := "RTP/AVP", ""
	if offer.tcp {
		transport = "TCP/RTP/AVP"
		setup = "a=setup:passive\r\na=connection:new\r\n"
		if offer.passive {
			setup = "a=setup:active\r\na=connection:new\r\n"
		}
	}
	sdp := fmt.Sprintf("v=0\r\no=%s 0 0 IN IP4 %s\r\ns=Play\r\nc=IN IP4 %s\r\nt=0 0\r\nm=audio %d %s 8\r\na=rtpmap:8 PCMA/8000\r\na=sendonly\r\n%sy=%010d\r\nf=v/a/1/8/1\r\n", g.cfg.ServerID, g.cfg.PublicIP, g.cfg.PublicIP, t.port, transport, setup, t.ssrc)
	if err = dialog.RespondSDP([]byte(sdp)); err != nil {
		t.Close()
		return
	}
	var conn net.Conn
	if offer.tcp {
		if offer.passive {
			conn, err = net.DialTimeout("tcp4", net.JoinHostPort(t.source.String(), strconv.Itoa(offer.port)), 5*time.Second)
		} else {
			_ = t.tcp.SetDeadline(time.Now().Add(8 * time.Second))
			for {
				var c *net.TCPConn
				c, err = t.tcp.AcceptTCP()
				if err != nil {
					break
				}
				if c.RemoteAddr().(*net.TCPAddr).AddrPort().Addr().Unmap() != t.source.Unmap() {
					c.Close()
					continue
				}
				_ = c.SetNoDelay(true)
				conn = c
				break
			}
		}
		if err != nil {
			t.Close()
			return
		}
	}
	t.mu.Lock()
	select {
	case <-t.Done:
		t.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
		return
	default:
	}
	t.conn = conn
	if !offer.tcp {
		t.remote = &net.UDPAddr{IP: net.IP(t.source.AsSlice()), Port: offer.port}
	}
	close(t.Ready)
	t.mu.Unlock()
}

func (g *Gateway) talkDialog(req *sip.Request) *Talk {
	if req.CallID() == nil {
		return nil
	}
	source, err := netip.ParseAddrPort(req.Source())
	if err != nil {
		return nil
	}
	id, err := sip.DialogIDFromRequestUAS(req)
	if err != nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, t := range g.talks {
		t.mu.Lock()
		match := t.source == source.Addr() && (strings.EqualFold(req.Transport(), "TCP") || t.signaling == source) && t.dialog != nil && t.dialog.ID == id
		t.mu.Unlock()
		if match {
			return t
		}
	}
	return nil
}
func (g *Gateway) talkAck(req *sip.Request, tx sip.ServerTransaction) {
	if t := g.talkDialog(req); t != nil {
		t.mu.Lock()
		d := t.dialog
		tx := t.transaction
		t.mu.Unlock()
		if tx != nil {
			tx.Terminate()
		}
		_ = d.ReadAck(req, tx)
	}
}
func (g *Gateway) talkBye(req *sip.Request, tx sip.ServerTransaction) bool {
	t := g.talkDialog(req)
	if t == nil {
		return false
	}
	t.mu.Lock()
	d := t.dialog
	t.mu.Unlock()
	if d.ReadBye(req, tx) != nil {
		respond(tx, req, 481, "Dialog Not Found")
	}
	t.Close()
	return true
}

func (t *Talk) WriteAudio(payload []byte) error {
	if len(payload) == 0 || len(payload) > 1600 {
		return errors.New("invalid audio frame")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-t.Done:
		return io.ErrClosedPipe
	default:
	}
	select {
	case <-t.Ready:
	default:
		return errors.New("audio not ready")
	}
	packet := rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 8, SequenceNumber: t.sequence, Timestamp: t.timestamp, SSRC: t.ssrc}, Payload: payload}
	t.sequence++
	t.timestamp += uint32(len(payload))
	raw, err := packet.Marshal()
	if err != nil {
		return err
	}
	if t.conn != nil {
		_ = t.conn.SetWriteDeadline(time.Now().Add(time.Second))
		frame := make([]byte, len(raw)+2)
		binary.BigEndian.PutUint16(frame, uint16(len(raw)))
		copy(frame[2:], raw)
		for len(frame) > 0 {
			n, e := t.conn.Write(frame)
			if e != nil {
				return e
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			frame = frame[n:]
		}
		return nil
	}
	if t.remote == nil {
		return errors.New("audio transport missing")
	}
	_ = t.udp.SetWriteDeadline(time.Now().Add(time.Second))
	_, err = t.udp.WriteToUDP(raw, t.remote)
	return err
}

func (t *Talk) Close() {
	t.once.Do(func() {
		close(t.Done)
		t.tcp.Close()
		t.udp.Close()
		t.mu.Lock()
		if t.conn != nil {
			t.conn.Close()
		}
		d := t.dialog
		t.mu.Unlock()
		t.g.mu.Lock()
		if t.g.talks[t.device] == t {
			delete(t.g.talks, t.device)
		}
		t.g.mu.Unlock()
		if d != nil {
			go func() {
				defer d.Close()
				if d.LoadState() == sip.DialogStateConfirmed {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					_ = d.Bye(ctx)
				}
			}()
		}
	})
}
