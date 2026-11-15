package integration

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"viewhub/apps/capture"
	"viewhub/apps/platform/gb"
)

func freeUDP(t *testing.T) int {
	t.Helper()
	c, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}
func until(t *testing.T, fn func() bool) {
	t.Helper()
	timeout := time.NewTimer(12 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if fn() {
			return
		}
		select {
		case <-timeout.C:
			t.Fatal("condition timed out")
		case <-tick.C:
		}
	}
}

// Real platform and device sockets verify digest registration, XML catalog,
// empty recording results, deregistration and restart without a USB dependency.
func TestDeviceAgainstViewHub(t *testing.T) {
	store, e := gb.OpenStore(filepath.Join(t.TempDir(), "platform.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	c := capture.DefaultConfig()
	c.Port = freeUDP(t)
	c.SIPPort = freeUDP(t)
	c.LocalIP = "127.0.0.1"
	c.Password = "test-private-password"
	if e = store.SeedDevice(c.DeviceID, "USB fixture", "camera", c.Password); e != nil {
		t.Fatal(e)
	}
	gateway, e := gb.New(gb.Config{PublicIP: "127.0.0.1", SIPPort: c.Port, ServerID: c.ServerID, Realm: c.Realm}, store)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	platformDone := make(chan error, 1)
	go func() { platformDone <- gateway.Serve(ctx) }()
	defer func() { cancel(); <-platformDone }()
	d, _ := capture.NewDevice(filepath.Join(t.TempDir(), "config.json"))
	if e = d.Save(c); e != nil {
		t.Fatal(e)
	}
	defer d.Stop()
	for range 5 {
		if e = d.Start(); e != nil {
			t.Fatal(e)
		}
		until(t, func() bool { return d.Snapshot().Registered })
		for _, event := range d.Snapshot().Events {
			if strings.Contains(event.Message, "bind:") {
				t.Fatalf("listener raced with registration: %s", event.Message)
			}
		}
		until(t, func() bool {
			devices, e := gateway.Devices(gb.DefaultSpaceID)
			return e == nil && len(devices) == 1 && len(devices[0].Channels) == 1 && devices[0].Channels[0].ID == c.ChannelID
		})
		timeout, stop := context.WithTimeout(context.Background(), 3*time.Second)
		records, e := gateway.Records(timeout, c.DeviceID, c.ChannelID, time.Now().Add(-time.Hour), time.Now())
		stop()
		if e != nil || len(records) != 0 {
			t.Fatalf("empty recording query: %v %v", records, e)
		}
		d.Stop()
		if d.Snapshot().Running {
			t.Fatal("device did not stop")
		}
		if _, e = gateway.Address(c.DeviceID); e == nil {
			t.Fatal("device did not deregister")
		}
	}
}
