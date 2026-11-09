package edge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReplayTicketBindsViewerChannelAndBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/site/camera/replay-control", strings.NewReader(`{"slot":"watch","revision":2,"action":"start"}`))
	if err := signReplayRequest(req, "edge-secret", "viewer"); err != nil {
		t.Fatal(err)
	}
	if cmd, ok := readReplayTicket(req, "edge-secret"); !ok || cmd.Revision != 2 {
		t.Fatal("valid ticket rejected")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/site/camera/replay-control", strings.NewReader(`{"slot":"watch","revision":2,"action":"start"}`))
	if err := signReplayRequest(req, "edge-secret", "viewer"); err != nil {
		t.Fatal(err)
	}
	req.URL.Path = "/api/site/another/replay-control"
	if _, ok := readReplayTicket(req, "edge-secret"); ok {
		t.Fatal("ticket accepted for another channel")
	}
	now := time.Now()
	for _, age := range []time.Duration{4 * time.Second, -2 * time.Second} {
		if ticketTimeValid(now, now.Add(-age)) {
			t.Fatalf("ticket accepted with age %v", age)
		}
	}
}

func TestReplayLeaseRequiresFreshFramesAndPosition(t *testing.T) {
	now := time.Now()
	s := replaySession{deadline: now.Add(replayLifetime), mode: "rtc", position: 5000, frames: 3, sequence: 1, lastProgress: now.Add(-time.Second), source: replaySource{end: 40}}
	cmd := replayCommand{Transport: "rtc", Sequence: 2, Frames: 4, Position: 6000}
	if !s.renew(cmd, now) {
		t.Fatal("valid progress rejected")
	}
	deadline := s.deadline
	for _, rejected := range []replayCommand{
		cmd,
		{Transport: "rtc", Sequence: 3, Frames: 4, Position: 6500},
		{Transport: "rtc", Sequence: 3, Frames: 5, Position: 6000},
		{Transport: "mp4", Sequence: 3, Frames: 5, Position: 6500},
		{Transport: "rtc", Sequence: 3, Frames: 5, Position: 30000},
	} {
		if s.renew(rejected, now) || s.deadline != deadline {
			t.Fatalf("invalid renewal advanced lease: %+v", rejected)
		}
	}
	s.timer.Stop()
	cmd.Sequence, cmd.Frames, cmd.Position = 3, 5, 6500
	if s.renew(cmd, deadline) || !s.closed {
		t.Fatal("expired lease resurrected")
	}
}

func TestReplayExpiryAndGenerationCancelUpstream(t *testing.T) {
	var r replayRegistry
	old := r.get("viewer", "camera", replayCommand{Slot: "watch", Revision: 1}, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	old.mu.Lock()
	old.cancel, old.done = cancel, done
	old.mu.Unlock()
	current := r.get("viewer", "camera", replayCommand{Slot: "watch", Revision: 2}, true)
	if current == nil || current.previous != done || ctx.Err() == nil {
		t.Fatal("new generation failed to cancel and wait for old upstream")
	}
	if r.get("viewer", "camera", replayCommand{Slot: "watch", Revision: 1}, true) != nil {
		t.Fatal("old generation was accepted")
	}
	if r.get("other", "camera", replayCommand{Slot: "watch", Revision: 2}, false) != nil {
		t.Fatal("another viewer obtained playback")
	}
	stopCtx, stop := context.WithCancel(context.Background())
	current.mu.Lock()
	current.cancel = stop
	current.arm(20 * time.Millisecond)
	current.mu.Unlock()
	select {
	case <-stopCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("edge watchdog did not cancel upstream")
	}
	close(done)
}
