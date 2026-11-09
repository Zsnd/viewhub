package main

import (
	"context"
	"testing"
	"time"
)

func TestViewerLeaseRequiresTimelyRenewal(t *testing.T) {
	now := time.Now()
	f := &feed{}
	m := sessions{viewers: map[string]viewer{"v": {feed: f, expires: now.Add(viewerTTL), lastProgress: now}}}
	for _, sec := range []int{4, 8, 12, 16, 20, 24, 28, 32} {
		if !m.renew("v", "", false, now.Add(time.Duration(sec)*time.Second)) {
			t.Fatal("preparation ended early")
		}
	}
	if m.renew("v", "", true, now.Add(preparingLimit)) {
		t.Fatal("expired viewer renewed")
	}
	m.viewers["v"] = viewer{feed: f, expires: now.Add(viewerTTL), lastProgress: now}
	if !m.renew("v", "", true, now.Add(8*time.Second)) {
		t.Fatal("progress was not accepted")
	}
	if got := m.viewers["v"].expires.Sub(now); got != 21*time.Second {
		t.Fatalf("expiry = %s", got)
	}
}

func TestOnlyLastViewerStopsSharedFeed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &feed{cancel: cancel, done: make(chan struct{})}
	m := sessions{viewers: map[string]viewer{"a": {feed: f, key: "live"}, "b": {feed: f, key: "live"}}}
	if m.stop("a", "", "test") != nil || ctx.Err() != nil {
		t.Fatal("first viewer stopped the feed")
	}
	if m.stop("b", "", "test") != f.done || ctx.Err() == nil || !f.closing {
		t.Fatal("last viewer did not stop the feed")
	}
}
