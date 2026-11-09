package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"viewhub/internal/frigaterelay"
	"viewhub/internal/gb"
)

func TestFrigateRelayClosesOnStalePlaybackProgress(t *testing.T) {
	upstreamClosed := make(chan time.Time, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte("video")); err != nil {
			return
		}
		_, _, _ = conn.ReadMessage()
		upstreamClosed <- time.Now()
	}))
	defer upstream.Close()

	frigate, err := newFrigate(upstream.URL, "hik_bullet")
	if err != nil {
		t.Fatal(err)
	}
	edge := httptest.NewServer(frigaterelay.Edge(frigate.origin, frigate.camera))
	defer edge.Close()
	frigate.relayOrigin, err = url.Parse(edge.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &application{frigate: frigate}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), identityKey{}, identity{account: gb.Account{SpaceID: gb.DefaultSpaceID}})
		app.frigateLive(w, r.WithContext(ctx))
	}))
	defer gateway.Close()

	browser, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	browser.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := browser.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if err := browser.WriteJSON(map[string]int{"frames": 15}); err != nil {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Second)
	lastProgress := time.Now()
	if err := browser.WriteJSON(map[string]int{"frames": 30}); err != nil {
		t.Fatal(err)
	}

	duplicates := time.NewTicker(100 * time.Millisecond)
	defer duplicates.Stop()
	deadline := time.NewTimer(frigaterelay.ProgressTimeout + 2*time.Second)
	defer deadline.Stop()
	for {
		select {
		case closed := <-upstreamClosed:
			elapsed := closed.Sub(lastProgress)
			if elapsed < frigaterelay.ProgressTimeout-100*time.Millisecond {
				t.Fatalf("progress did not extend the relay lease: closed after %s", elapsed)
			}
			return
		case <-duplicates.C:
			_ = browser.WriteJSON(map[string]int{"frames": 30})
		case <-deadline.C:
			t.Fatal("duplicate frame counts kept the relay alive")
		}
	}
}
