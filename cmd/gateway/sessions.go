package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"viewhub/internal/gb"
	"viewhub/internal/media"
)

const viewerTTL = 13 * time.Second
const preparingLimit = 36 * time.Second

type feed struct {
	closing bool
	stream  *media.Stream
	cancel  context.CancelFunc
	done    chan struct{}
}
type viewer struct {
	owner        string
	accountID    int64
	feed         *feed
	key          string
	expires      time.Time
	lastProgress time.Time
	done         chan struct{}
}
type sessions struct {
	mu          sync.Mutex
	gb          *gb.Gateway
	first, last int
	feeds       map[string]*feed
	viewers     map[string]viewer
	sequence    uint32
}

func token() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func (m *sessions) start(request context.Context, actor identity, id, channel string, start, end time.Time) (string, error) {
	address, err := m.gb.Address(id)
	if err != nil {
		return "", err
	}
	if !m.gb.Store.HasChannel(actor.account.SpaceID, id, channel) {
		return "", errors.New("unknown channel")
	}
	key := id + "/" + channel
	if !start.IsZero() {
		key += "/" + token()
	}
	var f *feed
	for {
		m.mu.Lock()
		if err := request.Err(); err != nil {
			m.mu.Unlock()
			return "", err
		}
		f = m.feeds[key]
		if f == nil || !f.closing {
			break
		}
		done := f.done
		m.mu.Unlock()
		select {
		case <-request.Done():
			return "", request.Err()
		case <-done:
		}
	}
	defer m.mu.Unlock()
	if f == nil {
		stream, err := media.Open(m.first, m.last)
		if err != nil {
			return "", err
		}
		m.sequence = (m.sequence + 1) % 9999
		kind := 0
		if !start.IsZero() {
			kind = 1
		}
		ssrc := fmt.Sprintf("%d00000%04d", kind, m.sequence)
		ctx, cancel := context.WithCancel(context.Background())
		f = &feed{stream: stream, cancel: cancel, done: make(chan struct{})}
		m.feeds[key] = f
		go func(f *feed) {
			defer close(f.done)
			mediaCtx, stopMedia := context.WithCancel(context.Background())
			defer stopMedia()
			mediaDone := make(chan struct{})
			go func() { defer close(mediaDone); stream.Run(mediaCtx, address.Addr()) }()
			dial, dialCancel := context.WithTimeout(ctx, 8*time.Second)
			dialog, err := m.gb.Invite(dial, address, id, channel, stream.Port(), ssrc, start, end)
			dialCancel()
			if err != nil {
				slog.Warn("SIP invite", "device", id, "error", err)
			} else {
				timer := time.NewTimer(12 * time.Second)
				select {
				case <-ctx.Done():
				case <-dialog.Context().Done():
				case <-timer.C:
					slog.Warn("no video", "device", id)
				case <-stream.Ready:
					select {
					case <-ctx.Done():
					case <-dialog.Context().Done():
					case <-mediaDone:
					}
				}
				timer.Stop()
				requested := time.Now()
				stop, release := context.WithTimeout(context.Background(), 2*time.Second)
				err := dialog.Bye(stop)
				release()
				code := 200
				if err != nil {
					code = 0
					var response sipgo.ErrDialogResponse
					if errors.As(err, &response) {
						code = response.Res.StatusCode
					}
				}
				// Keep receiving briefly so device-side traffic is observable after BYE.
				time.Sleep(1500 * time.Millisecond)
				lastRTP := stream.Metrics.LastRTPUnixNano.Load()
				quiet := lastRTP != 0 && time.Since(time.Unix(0, lastRTP)) >= time.Second
				slog.Info("GB stop observed", "device", id, "channel", channel, "bye_status", code,
					"bye_error", err, "rtp_quiet", quiet, "rtp_after_bye", lastRTP > requested.UnixNano(),
					"last_rtp_unix_nano", lastRTP)
				dialog.Close()
			}
			cancel()
			stopMedia()
			<-mediaDone
			slog.Info("media session ended", "device", id, "frames", stream.Metrics.Frames.Load(), "rtp_missing", stream.Metrics.Missing.Load(),
				"demux_max_gap_ms", stream.Metrics.InputGapMS.Load(), "send_max_gap_ms", stream.Metrics.OutputGapMS.Load())
			m.mu.Lock()
			if m.feeds[key] == f {
				delete(m.feeds, key)
			}
			for id, v := range m.viewers {
				if v.feed == f {
					m.removeViewer(id, v)
				}
			}
			m.mu.Unlock()
		}(f)
	}
	session := token()
	now := time.Now()
	m.viewers[session] = viewer{owner: actor.owner, accountID: actor.account.ID, feed: f, key: key, expires: now.Add(viewerTTL), lastProgress: now, done: make(chan struct{})}
	return session, nil
}
func (m *sessions) renew(id, owner string, progressing bool, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[id]
	if !ok || v.owner != owner || !now.Before(v.expires) {
		return false
	}
	if progressing {
		v.lastProgress = now
	}
	limit := v.lastProgress.Add(preparingLimit)
	if !now.Before(limit) {
		return false
	}
	v.expires = now.Add(viewerTTL)
	if limit.Before(v.expires) {
		v.expires = limit
	}
	m.viewers[id] = v
	return true
}

func (m *sessions) stop(id, owner, reason string) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.viewers[id]; !ok || v.owner != owner {
		return nil
	}
	return m.stopLocked(id, reason)
}

func (m *sessions) stopLocked(id string, reason string) <-chan struct{} {
	v, ok := m.viewers[id]
	if !ok {
		return nil
	}
	m.removeViewer(id, v)
	for _, other := range m.viewers {
		if other.feed == v.feed {
			return nil
		}
	}
	v.feed.closing = true
	slog.Info("last viewer left", "stream", v.key, "reason", reason)
	v.feed.cancel()
	return v.feed.done
}
func (m *sessions) reap(ctx context.Context) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			for _, f := range m.feeds {
				f.cancel()
			}
			m.mu.Unlock()
			return
		case now := <-timer.C:
			m.mu.Lock()
			for id, v := range m.viewers {
				if now.After(v.expires) {
					m.stopLocked(id, "lease_expired")
				}
			}
			m.mu.Unlock()
		}
	}
}

// removeViewer is called with m.mu held.
func (m *sessions) removeViewer(id string, v viewer) {
	delete(m.viewers, id)
	if v.done != nil {
		close(v.done)
	}
}

func (m *sessions) getViewer(id, owner string) (viewer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[id]
	return v, ok && v.owner == owner && time.Now().Before(v.expires)
}

func (m *sessions) getWithoutRenew(id, owner string) *feed {
	v, ok := m.getViewer(id, owner)
	if !ok {
		return nil
	}
	return v.feed
}

func (m *sessions) revoke(owner string, accountID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, v := range m.viewers {
		if (owner != "" && v.owner == owner) || (accountID != 0 && v.accountID == accountID) {
			m.stopLocked(id, "login_revoked")
		}
	}
}
