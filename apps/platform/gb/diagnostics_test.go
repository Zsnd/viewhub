package gb

import (
	"encoding/base64"
	"encoding/xml"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

func TestCatalogPTZCapabilities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "viewhub.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	const device = "34020000001320000001"
	if err = store.SeedDevice(device, "Camera", "camera", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	// Simulate a database created before catalog capabilities were stored.
	if _, err = store.db.Exec("DROP TABLE ptz_catalog"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var msg message
	err = xml.Unmarshal([]byte(`<Response><CmdType>Catalog</CmdType><DeviceList>
 <Item><DeviceID>34020000001310000001</DeviceID><Info><PTZType>1</PTZType></Info></Item>
 <Item><DeviceID>34020000001310000002</DeviceID><Info><PTZType>2</PTZType></Info></Item>
 <Item><DeviceID>34020000001310000003</DeviceID><Info><PTZType>3</PTZType></Info></Item>
 <Item><DeviceID>34020000001310000004</DeviceID><Info><PTZType>4</PTZType></Info></Item>
 <Item><DeviceID>34020000001310000005</DeviceID><Info><PTZType>5</PTZType></Info></Item>
 <Item><DeviceID>34020000001310000006</DeviceID></Item>
 <Item><DeviceID>34020000001310000007</DeviceID><Info><PTZType>unknown</PTZType></Info></Item>
 </DeviceList></Response>`), &msg)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.saveChannels(device, msg.Channels); err != nil {
		t.Fatal(err)
	}
	devices, err := store.devices(DefaultSpaceID)
	if err != nil || len(devices) != 1 || len(devices[0].Channels) != 7 {
		t.Fatalf("catalog devices: %+v, %v", devices, err)
	}
	for i, channel := range devices[0].Channels {
		want := i == 0 || i == 3 || i == 4
		source := "catalog"
		if i >= 5 {
			source = "unknown"
		}
		if channel.PTZ != want || channel.PTZSource != source || store.CanPTZ(DefaultSpaceID, device, channel.ID) != want {
			t.Errorf("capability: %+v, want enabled=%v source=%s", channel, want, source)
		}
		if store.CanPTZ(DefaultSpaceID+1, device, channel.ID) {
			t.Error("another space can control channel")
		}
	}
	first, fixed := msg.Channels[0].ID, msg.Channels[2].ID
	if err = store.SetPTZ(DefaultSpaceID, device, first, false); err != nil {
		t.Fatal(err)
	}
	if err = store.SetPTZ(DefaultSpaceID, device, fixed, true); err != nil {
		t.Fatal(err)
	}
	if err = store.saveChannels(device, msg.Channels); err != nil {
		t.Fatal(err)
	}
	if store.CanPTZ(DefaultSpaceID, device, first) || !store.CanPTZ(DefaultSpaceID, device, fixed) {
		t.Fatal("catalog refresh overwrote manual settings")
	}
	devices, err = store.devices(DefaultSpaceID)
	if err != nil || devices[0].Channels[0].PTZSource != "manual" || devices[0].Channels[2].PTZSource != "manual" {
		t.Fatalf("manual capability sources: %+v, %v", devices, err)
	}
	if err = store.ResetPTZ(DefaultSpaceID+1, device, first); err == nil {
		t.Fatal("another space reset the channel capability")
	}
	if err = store.ResetPTZ(DefaultSpaceID, device, first); err != nil {
		t.Fatal(err)
	}
	if !store.CanPTZ(DefaultSpaceID, device, first) {
		t.Fatal("reset did not restore catalog capability")
	}
}

func TestRegistrationNonceFitsCameraBuffer(t *testing.T) {
	g := &Gateway{cfg: Config{Realm: "3402000000"}}
	addr := netip.MustParseAddr("192.0.2.10")
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := g.nonce(addr, stamp)
	if len(nonce) >= 64 {
		t.Fatalf("nonce needs %d bytes plus a terminator, exceeding camera buffer", len(nonce))
	}
	issued, signature, ok := strings.Cut(nonce, ".")
	mac, err := base64.RawURLEncoding.DecodeString(signature)
	if !ok || issued != stamp || err != nil || len(mac) != 32 {
		t.Fatalf("nonce must retain timestamp and full SHA-256 MAC")
	}
	const id = "34020000001320000001"
	const password = "fixture-sip-password"
	for _, tc := range []struct{ name, nonce string }{
		{"valid", nonce},
		{"different_address", g.nonce(netip.MustParseAddr("192.0.2.11"), stamp)},
		{"expired", g.nonce(addr, strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10))},
		{"modified", nonce[:len(nonce)-1] + "!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := sip.NewRequest(sip.REGISTER, sip.Uri{Scheme: "sip", User: "34020000002000000002", Host: g.cfg.Realm})
			credentials, err := digest.Digest(&digest.Challenge{Realm: g.cfg.Realm, Nonce: tc.nonce, Algorithm: "MD5"}, digest.Options{
				Method: "REGISTER", URI: req.Recipient.String(), Username: id, Password: password,
			})
			if err != nil {
				t.Fatal(err)
			}
			req.AppendHeader(sip.NewHeader("Authorization", credentials.String()))
			want := "stale_challenge"
			if tc.name == "valid" {
				want = ""
			}
			if got := g.authFailure(req, addr, id, password); got != want {
				t.Fatalf("auth failure = %q, want %q", got, want)
			}
		})
	}
}

func TestRegistrationDiagnostics(t *testing.T) {
	const id = "34020000001180000011"
	const password = "fixture-sip-password"
	g := &Gateway{cfg: Config{Realm: "3402000000"}, attempts: map[string]registerAttempt{}, online: map[string]registration{}}
	now := time.Now()
	if online, d := g.diagnostic(id, now); online || d.Registration != "waiting" || d.Failure != "" {
		t.Fatalf("new device: %+v", d)
	}
	addr := netip.MustParseAddr("127.0.0.1")
	challenge := &digest.Challenge{Realm: g.cfg.Realm, Nonce: g.nonce(addr, strconv.FormatInt(now.Unix(), 10)), Algorithm: "MD5"}
	for _, tc := range []struct{ password, reason string }{{"", "challenge"}, {"wrong-password", "bad_password"}, {password, ""}} {
		req := sip.NewRequest(sip.REGISTER, sip.Uri{Scheme: "sip", User: "34020000002000000002", Host: g.cfg.Realm})
		if tc.password != "" {
			credentials, err := digest.Digest(challenge, digest.Options{Method: "REGISTER", URI: req.Recipient.String(), Username: id, Password: tc.password})
			if err != nil {
				t.Fatal(err)
			}
			req.AppendHeader(sip.NewHeader("Authorization", credentials.String()))
		}
		reason := g.authFailure(req, addr, id, password)
		if reason != tc.reason {
			t.Fatalf("registration reason %q, want %q", reason, tc.reason)
		}
		g.rememberRegister(id, reason)
	}
	g.online[id] = registration{seen: now, expires: now.Add(time.Hour)}
	if online, d := g.diagnostic(id, now); !online || d.Registration != "online" || d.Failure != "" || d.LastSignal == nil {
		t.Fatalf("registered device: %+v", d)
	}
	if online, d := g.diagnostic(id, now.Add(191*time.Second)); online || d.Failure != "heartbeat_timeout" {
		t.Fatalf("missing heartbeat: %+v", d)
	}
}

func TestMultipartCatalogKeepsLastCompleteList(t *testing.T) {
	const id = "34020000001180000011"
	const first = "34020000001320000001"
	const second = "34020000001320000002"
	const parent = "34020000002160000001"
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SeedDevice(id, "NVR", "recorder", "fixture-sip-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.saveChannels(id, []Channel{{ID: first, Name: "旧通道名称"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	g := &Gateway{Store: store, online: map[string]registration{id: {seen: now, expires: now.Add(time.Hour)}},
		catalogs: map[string]*catalogAttempt{id: {SN: 12, State: "querying", Deadline: now.Add(catalogTimeout), Items: map[string]Channel{}}}}
	packet := message{SN: 12, Total: 3, Channels: []Channel{{ID: first, Name: "新通道名称"}}}
	g.receiveCatalog(id, packet)
	g.receiveCatalog(id, packet)
	g.receiveCatalog(id, message{SN: 11, Total: 1, Channels: []Channel{{ID: second}}})
	_, d := g.diagnostic(id, now.Add(catalogTimeout))
	devices, err := store.devices(DefaultSpaceID)
	if err != nil || d.Catalog != "timeout" || d.CatalogReceived != 1 || d.CatalogTotal != 3 || len(devices[0].Channels) != 1 || devices[0].Channels[0].Name != "旧通道名称" {
		t.Fatalf("incomplete catalog: %+v, %+v, %v", d, devices, err)
	}
	g.receiveCatalog(id, message{SN: 12, Total: 3, Channels: []Channel{{ID: parent, Parental: 1}, {ID: second, Name: "通道 2"}}})
	devices, err = g.Devices(DefaultSpaceID)
	if err != nil || len(devices[0].Channels) != 2 || devices[0].Channels[0].Name != "新通道名称" || devices[0].Diagnostics.Catalog != "ready" {
		t.Fatalf("complete catalog: %+v, %v", devices, err)
	}
	g.catalogs[id] = &catalogAttempt{SN: 13, State: "querying", Deadline: now.Add(catalogTimeout), Items: map[string]Channel{}}
	g.receiveCatalog(id, message{SN: 13})
	devices, err = g.Devices(DefaultSpaceID)
	if err != nil || len(devices[0].Channels) != 0 || devices[0].Diagnostics.Catalog != "ready" {
		t.Fatalf("empty catalog: %+v, %v", devices, err)
	}
}
