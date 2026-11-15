package gb

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHeldPTZExpiresAndStops(t *testing.T) {
	state := &controlState{}
	sent := make(chan string, 8)
	send := func(_ context.Context, code string) error { sent <- code; return nil }
	move, _ := commandBytes("left", 0)
	stop, _ := commandBytes("stop", 0)
	if err := state.hold(context.Background(), move, send, 40*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := <-sent; got != move {
		t.Fatalf("movement = %s", got)
	}
	select {
	case got := <-sent:
		if got != stop {
			t.Fatalf("expiry = %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("lost client did not stop")
	}
}

func TestHeldPTZRenewalAndExplicitStopCancelExpiry(t *testing.T) {
	state := &controlState{}
	sent := make(chan string, 8)
	send := func(_ context.Context, code string) error { sent <- code; return nil }
	move, _ := commandBytes("right", 0)
	stop, _ := commandBytes("stop", 0)
	if err := state.hold(context.Background(), move, send, time.Second); err != nil {
		t.Fatal(err)
	}
	<-sent
	if err := state.hold(context.Background(), move, send, 40*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	<-sent
	state.mu.Lock()
	state.cancelLease()
	_ = send(context.Background(), stop)
	state.mu.Unlock()
	if got := <-sent; got != stop {
		t.Fatal("explicit stop missing")
	}
	select {
	case <-sent:
		t.Fatal("cancelled expiry sent an extra command")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHeldPTZFailedMovementStillStops(t *testing.T) {
	state := &controlState{}
	move, _ := commandBytes("up", 0)
	stop, _ := commandBytes("stop", 0)
	var calls []string
	send := func(_ context.Context, code string) error {
		calls = append(calls, code)
		if code == move {
			return errors.New("response lost")
		}
		return nil
	}
	if err := state.hold(context.Background(), move, send, time.Second); err == nil {
		t.Fatal("expected movement failure")
	}
	if len(calls) != 2 || calls[1] != stop {
		t.Fatalf("commands = %v", calls)
	}
}

func TestHeldPTZRenewalReplacesPreviousDeadline(t *testing.T) {
	state := &controlState{}
	sent := make(chan string, 8)
	send := func(_ context.Context, code string) error { sent <- code; return nil }
	move, _ := commandBytes("down", 0)
	stop, _ := commandBytes("stop", 0)
	if err := state.hold(context.Background(), move, send, 40*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	<-sent
	if err := state.hold(context.Background(), move, send, 180*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	<-sent
	select {
	case <-sent:
		t.Fatal("old deadline stopped the renewed movement")
	case <-time.After(80 * time.Millisecond):
	}
	select {
	case got := <-sent:
		if got != stop {
			t.Fatalf("expiry = %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("renewed lease did not expire")
	}
}
