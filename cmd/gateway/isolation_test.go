package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"viewhub/internal/gb"
)

func TestCustomerIsolationAndLegacyDeviceMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const adminDevice = "34020000001320000002"
	const customerA = "34020000001180000011"
	const customerB = "34020000001180000012"
	const ch = "34020000001320000001"
	_, err = db.Exec(`
 CREATE TABLE devices(id TEXT PRIMARY KEY,name TEXT NOT NULL,kind TEXT NOT NULL,secret TEXT NOT NULL);
 CREATE TABLE channels(device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,id TEXT NOT NULL,name TEXT NOT NULL,PRIMARY KEY(device_id,id));
 INSERT INTO devices VALUES('34020000001320000002','海康枪机','camera','original-sip-secret');
 INSERT INTO channels VALUES('34020000001320000002','34020000001320000001','Camera 01');
 `)
	if err != nil {
		t.Fatal(err)
	}
	store, err := gb.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.EnsureAdmin("viewer", "admin-original-pass"); err != nil {
		t.Fatal(err)
	}
	if err = store.SeedDevice(adminDevice, "should not replace", "camera", "seed-sip-secret"); err != nil {
		t.Fatal(err)
	}
	gateway, err := gb.New(gb.Config{PublicIP: "127.0.0.1", SIPPort: 5060, Realm: "3402000000", ServerID: "34020000002000000002"}, store)
	if err != nil {
		t.Fatal(err)
	}
	mgr := &sessions{gb: gateway, feeds: map[string]*feed{}, viewers: map[string]viewer{}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/stats":
			io.WriteString(w, `{"cameras":{"hik_bullet":{"camera_fps":5}}}`)
		case "/api/hik_bullet/recordings":
			io.WriteString(w, `[{"start_time":1793923200,"end_time":1793923210}]`)
		case "/vod/hik_bullet/start/1793923200/end/1793923210/index.m3u8":
			if r.Header.Get("Cookie") != "" {
				t.Error("gateway session leaked to Frigate")
			}
			io.WriteString(w, "#EXTM3U")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	frigate, err := newFrigate(upstream.URL, "hik_bullet")
	if err != nil {
		t.Fatal(err)
	}
	app := &application{frigate: frigate, gateway: gateway, sessions: mgr, sipAddress: "127.0.0.1", sipPort: 5060, serverID: "34020000002000000002", realm: "3402000000"}
	server := httptest.NewServer(app.protect(app.routes()))
	defer server.Close()
	client := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	guest, admin, a, b := client(), client(), client(), client()
	req := func(client *http.Client, method, path string, body any, want int) []byte {
		t.Helper()
		var input io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			input = bytes.NewReader(raw)
		}
		request, err := http.NewRequest(method, server.URL+path, input)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Playback-Request", "1")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s %s: status %d want %d, body %s", method, path, response.StatusCode, want, data)
		}
		return data
	}
	login := func(c *http.Client, user, pass string) {
		req(c, "POST", "/api/auth/login", map[string]string{"username": user, "password": pass}, 200)
	}
	req(guest, "GET", "/api/devices", nil, 401)
	req(guest, "POST", "/api/devices/"+adminDevice+"/setup", nil, 401)
	req(guest, "GET", "/api/session/fake/hls/index.m3u8", nil, 401)
	login(admin, "viewer", "admin-original-pass")
	for _, x := range []struct {
		c           *http.Client
		user, space string
	}{{a, "client-a", "空间 A"}, {b, "client-b", "空间 B"}} {
		req(admin, "POST", "/api/admin/accounts", map[string]string{"username": x.user, "space": x.space, "password": "first-setup-password"}, 201)
		login(x.c, x.user, "first-setup-password")
		req(x.c, "GET", "/api/devices", nil, 403)
		req(x.c, "POST", "/api/auth/password", map[string]string{"current": "first-setup-password", "password": "changed-" + x.user + "-password"}, 204)
		req(x.c, "GET", "/api/auth/me", nil, 401)
		login(x.c, x.user, "changed-"+x.user+"-password")
		req(x.c, "GET", "/api/admin/accounts", nil, 403)
		req(x.c, "POST", "/api/admin/accounts", map[string]string{"username": "elevate", "space": "越权", "password": "another-password"}, 403)
	}
	add := func(c *http.Client, id, name string, want int) {
		req(c, "POST", "/api/devices", map[string]string{"id": id, "name": name, "kind": "recorder", "secret": "fixture-sip-password"}, want)
	}
	add(a, customerA, "A 的录像机", 204)
	add(b, customerB, "B 的录像机", 204)
	add(b, customerA, "尝试覆盖", 409)
	add(a, adminDevice, "尝试覆盖旧枪机", 409)
	for _, id := range []string{customerA, customerB} {
		if _, err = db.Exec("INSERT INTO channels VALUES(?,?,?)", id, ch, "相同编号的通道"); err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		c                *http.Client
		own, name, other string
	}{{admin, adminDevice, "海康枪机", customerA}, {a, customerA, "A 的录像机", customerB}, {b, customerB, "B 的录像机", customerA}} {
		var list struct{ Devices []gb.Device }
		inventory := req(x.c, "GET", "/api/devices", nil, 200)
		if bytes.Contains(inventory, []byte("sip-secret")) || bytes.Contains(inventory, []byte("sip-password")) {
			t.Fatal("inventory exposed a registration password")
		}
		if err = json.Unmarshal(inventory, &list); err != nil {
			t.Fatal(err)
		}
		if x.own == adminDevice {
			if len(list.Devices) != 2 || list.Devices[0].ID != frigateDevice || !list.Devices[0].Online {
				t.Fatalf("missing Frigate in default space: %+v", list)
			}
			list.Devices = list.Devices[1:]
		}
		if len(list.Devices) != 1 || list.Devices[0].ID != x.own || list.Devices[0].Name != x.name || len(list.Devices[0].Channels) != 1 {
			t.Fatalf("unexpected inventory: %+v", list)
		}
		var setup struct {
			DeviceID, Secret, SipAddress, SipID, SipRealm, Protocol, Transport string
			SipPort                                                            int
		}
		if err = json.Unmarshal(req(x.c, "POST", "/api/devices/"+x.own+"/setup", nil, 200), &setup); err != nil {
			t.Fatal(err)
		}
		secret := "fixture-sip-password"
		if x.own == adminDevice {
			secret = "original-sip-secret"
		}
		if setup.DeviceID != x.own || setup.Secret != secret || setup.SipAddress != app.sipAddress || setup.SipID != app.serverID || setup.SipRealm != app.realm || setup.SipPort != app.sipPort || setup.Protocol != "GB/T 28181-2016" || setup.Transport != "UDP" {
			t.Fatal("incorrect setup configuration")
		}
		req(x.c, "POST", "/api/devices/"+x.other+"/setup", nil, 404)
		req(x.c, "PUT", "/api/devices/"+x.other, map[string]string{"name": "blocked", "kind": "camera"}, 404)
		req(x.c, "PUT", "/api/devices/"+x.own, map[string]string{"name": "renamed", "kind": "recorder"}, 204)
		if !bytes.Contains(req(x.c, "GET", "/api/devices", nil, 200), []byte("renamed")) {
			t.Fatal("device edit was not persisted")
		}
		if !bytes.Contains(req(x.c, "POST", "/api/devices/"+x.own+"/setup", nil, 200), []byte(secret)) {
			t.Fatal("blank password replaced the previous password")
		}
		if x.own != adminDevice {
			req(x.c, "POST", "/api/devices/"+adminDevice+"/setup", nil, 404)
		}
		route := "/api/devices/" + x.other + "/" + ch
		window := "?start=2026-11-06T00:00:00Z&end=2026-11-07T00:00:00Z"
		req(x.c, "POST", route+"/play", map[string]any{}, 404)
		req(x.c, "POST", route+"/play", map[string]string{"start": "2026-11-06T00:00:00Z", "end": "2026-11-07T00:00:00Z"}, 404)
		req(x.c, "GET", route+"/records"+window, nil, 404)
		own := "/api/devices/" + x.own + "/" + ch
		req(x.c, "POST", own+"/play", map[string]any{}, 503)
		req(x.c, "GET", own+"/records"+window, nil, 502)
	}
	const frigateRoute = "/api/devices/frigate/hik_bullet"
	const recordWindow = "?start=2026-11-06T00:00:00Z&end=2026-11-07T00:00:00Z"
	const vod = "/api/frigate/vod/1793923200/1793923210/index.m3u8"
	req(guest, "GET", vod, nil, 401)
	for _, customer := range []*http.Client{a, b} {
		req(customer, "GET", frigateRoute+"/records"+recordWindow, nil, 404)
		req(customer, "POST", frigateRoute+"/play", map[string]any{}, 404)
		req(customer, "GET", "/api/frigate/live", nil, 404)
		req(customer, "GET", "/api/frigate/rtc", nil, 404)
		req(customer, "GET", vod, nil, 404)
	}
	req(admin, "POST", frigateRoute+"/play", map[string]any{}, 201)
	if !bytes.Contains(req(admin, "GET", frigateRoute+"/records"+recordWindow, nil, 200), []byte("Frigate")) {
		t.Fatal("missing Frigate recording")
	}
	if string(req(admin, "GET", vod, nil, 200)) != "#EXTM3U" {
		t.Fatal("unexpected Frigate playlist")
	}
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookies := a.Jar.Cookies(endpoint)
	if len(cookies) != 1 {
		t.Fatal("expected one session cookie")
	}
	key := gb.SessionKey(cookies[0].Value)
	f := &feed{cancel: func() {}, done: make(chan struct{})}
	mgr.viewers["a-play"] = viewer{owner: key, feed: f, expires: time.Now().Add(time.Minute)}
	req(b, "POST", "/api/session/a-play/heartbeat", map[string]bool{"progressing": true}, 404)
	req(b, "POST", "/api/session/a-play/whep", nil, 404)
	req(b, "GET", "/api/session/a-play/hls/index.m3u8", nil, 404)
	req(b, "DELETE", "/api/session/a-play", nil, 404)
	req(a, "POST", "/api/session/a-play/heartbeat", map[string]bool{"progressing": true}, 204)
	req(a, "POST", "/api/auth/logout", nil, 204)
	req(a, "GET", "/api/devices", nil, 401)
	if mgr.getWithoutRenew("a-play", key) != nil {
		t.Fatal("logout retained playback session")
	}
}
