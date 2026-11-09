package gb

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

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
