package edge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	replayLifetime        = 10 * time.Second
	replayShutdownReserve = 100 * time.Millisecond
	replayTicketAge       = 3 * time.Second
	replayTicketHeader    = "X-ViewHub-Replay-Ticket"
	replayOwnerHeader     = "X-ViewHub-Replay-Owner"
	replayTimeHeader      = "X-ViewHub-Replay-Time"
)

type replayCommand struct {
	Slot                           string
	Revision, At, Sequence, Frames int64
	Position                       int64
	Action, Transport, SDP         string
}

func replaySignature(secret, owner, path, stamp string, data []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s\n%s\n%s\n", owner, path, stamp)
	mac.Write(data)
	return mac.Sum(nil)
}

func ticketTimeValid(now, issued time.Time) bool {
	age := now.Sub(issued)
	return age >= -time.Second && age <= replayTicketAge
}

func signReplayRequest(r *http.Request, secret, owner string) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(data) > 65536 {
		return errUnsupported
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	r.Header.Set(replayOwnerHeader, owner)
	r.Header.Set(replayTimeHeader, stamp)
	r.Header.Set(replayTicketHeader, hex.EncodeToString(replaySignature(secret, owner, r.URL.Path, stamp, data)))
	return nil
}

func readReplayTicket(r *http.Request, secret string) (replayCommand, bool) {
	var command replayCommand
	data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	stamp := r.Header.Get(replayTimeHeader)
	millis, stampErr := strconv.ParseInt(stamp, 10, 64)
	sig, sigErr := hex.DecodeString(r.Header.Get(replayTicketHeader))
	owner := r.Header.Get(replayOwnerHeader)
	valid := err == nil && len(data) <= 65536 && stampErr == nil && sigErr == nil && owner != "" &&
		ticketTimeValid(time.Now(), time.UnixMilli(millis)) && hmac.Equal(sig, replaySignature(secret, owner, r.URL.Path, stamp, data)) &&
		json.Unmarshal(data, &command) == nil && identifier.MatchString(command.Slot) && command.Revision > 0
	return command, valid
}

type replayRegistry struct {
	mu       sync.Mutex
	sessions map[string]*replaySession
}

type replaySession struct {
	mu                             sync.Mutex
	revision                       int64
	channel                        string
	at, position, sequence, frames int64
	mode                           string
	closed                         bool
	deadline                       time.Time
	lastProgress                   time.Time
	timer                          *time.Timer
	peer                           *webrtc.PeerConnection
	track                          *webrtc.TrackLocalStaticSample
	source                         replaySource
	ctx                            context.Context
	cancel                         context.CancelFunc
	done, previous                 <-chan struct{}
	sourceError                    bool
}

func (s *replaySession) stopLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.peer != nil {
		go s.peer.Close()
	}
}

func (s *replaySession) check(now time.Time) bool {
	if !s.closed && !now.Before(s.deadline) {
		s.stopLocked()
	}
	return !s.closed
}

func (s *replaySession) arm(duration time.Duration) {
	s.deadline = time.Now().Add(duration)
	if s.timer != nil {
		s.timer.Stop()
	}
	deadline := s.deadline
	delay := duration
	if duration == replayLifetime {
		delay -= replayShutdownReserve
	}
	s.timer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.deadline.Equal(deadline) {
			s.stopLocked()
		}
	})
}

func (s *replaySession) renew(cmd replayCommand, now time.Time) bool {
	if !s.check(now) || s.mode == "" || cmd.Transport != s.mode || cmd.Sequence <= s.sequence || cmd.Frames <= s.frames ||
		cmd.Position <= s.position || cmd.Position > s.source.end*1000 || cmd.Position-s.position > 2000+now.Sub(s.lastProgress).Milliseconds()*2 {
		return false
	}
	s.sequence, s.frames, s.position = cmd.Sequence, cmd.Frames, cmd.Position
	s.lastProgress = now
	s.arm(replayLifetime)
	return true
}

func (r *replayRegistry) get(owner, channel string, cmd replayCommand, create bool) *replaySession {
	key := owner + "/" + cmd.Slot
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions == nil {
		r.sessions = map[string]*replaySession{}
	}
	old := r.sessions[key]
	if old != nil && old.revision == cmd.Revision && old.channel == channel {
		return old
	}
	if !create || old != nil && old.revision >= cmd.Revision || old == nil && len(r.sessions) >= 256 {
		return nil
	}
	var previous <-chan struct{}
	if old != nil {
		old.mu.Lock()
		old.stopLocked()
		previous = old.done
		if previous == nil {
			previous = old.previous
		}
		old.mu.Unlock()
	}
	s := &replaySession{revision: cmd.Revision, channel: channel, previous: previous}
	s.arm(20 * time.Second)
	r.sessions[key] = s
	time.AfterFunc(35*time.Second, func() { r.cleanup(key, s) })
	return s
}

func (r *replayRegistry) cleanup(key string, s *replaySession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions[key] != s {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.check(time.Now()) {
		time.AfterFunc(35*time.Second, func() { r.cleanup(key, s) })
		return
	}
	delete(r.sessions, key)
}

func waitReplaySource(ctx context.Context, previous <-chan struct{}) bool {
	if previous != nil {
		<-previous
	}
	return ctx.Err() == nil
}
