package main

import (
	"context"
	"github.com/pion/webrtc/v4"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"viewhub/apps/platform/media"
)

func TestAudioOnlyListenerAndOwnership(t *testing.T) {
	api := webrtc.NewAPI()
	client, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tr, err := client.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
	if err != nil {
		t.Fatal(err)
	}
	tr.SetCodecPreferences([]webrtc.RTPCodecParameters{{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMA, ClockRate: 8000, Channels: 1}, PayloadType: 8}})
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	audio, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMA, ClockRate: 8000, Channels: 1}, "audio", "test")
	video, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH265, ClockRate: 90000}, "video", "test")
	ready := make(chan struct{})
	close(ready)
	done := make(chan struct{})
	defer close(done)
	stream := &media.Stream{Ready: ready, AudioTrack: audio, Track: video}
	f := &feed{stream: stream, done: done}
	m := &sessions{viewers: map[string]viewer{"watch": {owner: "owner", feed: f, expires: time.Now().Add(time.Minute)}}}
	app := &application{rtc: api, sessions: m}
	request := func(owner string) *http.Request {
		r := httptest.NewRequest("POST", "/api/session/watch/whep?audio=1", strings.NewReader(offer.SDP))
		r.SetPathValue("token", "watch")
		return r.WithContext(context.WithValue(r.Context(), identityKey{}, identity{owner: owner}))
	}
	foreign := httptest.NewRecorder()
	app.whep(foreign, request("foreign"))
	if foreign.Code != 404 {
		t.Fatal("foreign listener admitted")
	}
	response := httptest.NewRecorder()
	app.whep(response, request("owner"))
	if response.Code != 201 {
		t.Fatalf("audio negotiation: %d %s", response.Code, response.Body.String())
	}
	answer := response.Body.String()
	if !strings.Contains(answer, "m=audio ") || strings.Contains(answer, "m=video ") || !strings.Contains(answer, "PCMA/8000") {
		t.Fatalf("not audio-only PCMA: %s", answer)
	}
	value, ok := app.listeners.Load("watch")
	if !ok {
		t.Fatal("missing listener")
	}
	listener := value.(*audioListener)
	remove := func(owner string) int {
		r := httptest.NewRequest("DELETE", response.Header().Get("Location"), nil)
		r.SetPathValue("token", "watch")
		r.SetPathValue("listener", listener.id)
		r = r.WithContext(context.WithValue(r.Context(), identityKey{}, identity{owner: owner}))
		w := httptest.NewRecorder()
		app.stopAudioListener(w, r)
		return w.Code
	}
	if remove("foreign") != 404 {
		t.Fatal("foreign listener closed")
	}
	if listener.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
		t.Fatal("foreign close affected owner")
	}
	if remove("owner") != 204 {
		t.Fatal("owner close failed")
	}
	if listener.pc.ConnectionState() != webrtc.PeerConnectionStateClosed {
		t.Fatal("audio peer retained")
	}
	if _, ok = app.listeners.Load("watch"); ok {
		t.Fatal("listener retained")
	}
}
