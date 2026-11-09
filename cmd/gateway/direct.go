package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type directCommand struct {
	ID     string
	Offer  string
	Delete string
}

type directResult struct {
	Answer  string
	Session string
}

type directBridge struct {
	Device, Channel, Token string
	jobs                   chan directCommand
	mu                     sync.Mutex
	pending                map[string]chan directResult
}

func loadDirect(path string) (*directBridge, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d := &directBridge{jobs: make(chan directCommand), pending: make(map[string]chan directResult)}
	if err := json.Unmarshal(b, d); err != nil {
		return nil, err
	}
	if len(d.Token) < 32 || len(d.Device) != 20 || len(d.Channel) != 20 {
		return nil, &os.PathError{Op: "validate direct connector config", Path: path, Err: os.ErrInvalid}
	}
	return d, nil
}

func (d *directBridge) edge(w http.ResponseWriter, r *http.Request) {
	if !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte("Bearer "+d.Token)) {
		w.WriteHeader(401)
		return
	}
	if r.Method == http.MethodGet {
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		select {
		case cmd := <-d.jobs:
			jsonReply(w, 200, cmd)
		case <-timer.C:
			w.WriteHeader(204)
		case <-r.Context().Done():
		}
		return
	}
	var result directResult
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&result) != nil {
		w.WriteHeader(400)
		return
	}
	d.mu.Lock()
	reply := d.pending[r.PathValue("job")]
	d.mu.Unlock()
	if reply == nil {
		w.WriteHeader(410)
		return
	}
	select {
	case reply <- result:
	default:
	}
	w.WriteHeader(204)
}

func (d *directBridge) call(ctx context.Context, cmd directCommand) directResult {
	cmd.ID = hex.EncodeToString(randomDirectID())
	reply := make(chan directResult, 1)
	d.mu.Lock()
	d.pending[cmd.ID] = reply
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.pending, cmd.ID)
		d.mu.Unlock()
	}()
	select {
	case d.jobs <- cmd:
	case <-ctx.Done():
		return directResult{}
	}
	select {
	case result := <-reply:
		return result
	case <-ctx.Done():
		return directResult{}
	}
}

func randomDirectID() []byte {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return id[:]
}

func (d *directBridge) sign(owner, session string) string {
	mac := hmac.New(sha256.New, []byte(d.Token))
	mac.Write([]byte("whep/" + owner + "/" + session))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *application) directPlayback(w http.ResponseWriter, r *http.Request) {
	d := a.direct
	actor := who(r)
	if d == nil || r.PathValue("id") != d.Device || r.PathValue("channel") != d.Channel ||
		!a.gateway.Store.HasChannel(actor.account.SpaceID, d.Device, d.Channel) {
		w.WriteHeader(404)
		return
	}
	if r.Method == http.MethodDelete {
		session, signature, ok := strings.Cut(r.PathValue("session"), ".")
		if !ok || !hmac.Equal([]byte(signature), []byte(d.sign(actor.owner, session))) {
			w.WriteHeader(404)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		d.call(ctx, directCommand{Delete: session})
		w.WriteHeader(204)
		return
	}
	offer, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || !strings.HasPrefix(string(offer), "v=0") {
		w.WriteHeader(400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	result := d.call(ctx, directCommand{Offer: string(offer)})
	if !strings.HasPrefix(result.Answer, "v=0") || result.Session == "" {
		problem(w, 503, "直连暂不可用")
		return
	}
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("X-Direct-Session", result.Session+"."+d.sign(actor.owner, result.Session))
	w.WriteHeader(201)
	_, _ = io.WriteString(w, result.Answer)
}
