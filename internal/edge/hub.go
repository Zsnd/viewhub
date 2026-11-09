package edge

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

type Site struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Space    int64  `json:"space"`
	Token    string `json:"token"`
	Provider string `json:"provider"`
}
type Channel struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Timezone        string `json:"timezone"`
	PTZ             bool   `json:"ptz"`
	History         bool   `json:"history"`
	RecordingOnly   bool   `json:"recordingOnly,omitempty"`
	ManagedRecorder bool   `json:"managedRecorder,omitempty"`
	RecorderID      string `json:"recorderId,omitempty"`
	RecorderName    string `json:"recorderName,omitempty"`
	DeviceKind      string `json:"deviceKind,omitempty"`
}
type Inventory struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Provider string    `json:"provider"`
	Online   bool      `json:"online"`
	Channels []Channel `json:"channels"`
}
type connection struct {
	Site
	broker   *broker
	link     *mediaLink
	proxy    *httputil.ReverseProxy
	mu       sync.Mutex
	channels []Channel
}
type Hub struct {
	mu           sync.RWMutex
	sites        map[string]*connection
	provisionDir string
	managed      []managedSite
}

func Open(path, provisionDir string) (*Hub, error) {
	h := &Hub{sites: map[string]*connection{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return h, h.loadManaged(provisionDir)
	}
	if err != nil {
		return nil, err
	}
	var sites []Site
	if err := json.Unmarshal(data, &sites); err != nil {
		return nil, err
	}
	for _, s := range sites {
		if !validSite(s) || h.sites[s.ID] != nil {
			return nil, errors.New("invalid ViewHub site configuration")
		}
		h.addSite(s)
	}
	return h, h.loadManaged(provisionDir)
}
func validSite(s Site) bool {
	return identifier.MatchString(s.ID) && s.Name != "" && s.Space > 0 && len(s.Token) >= 32 && (s.Provider == "edge" || s.Provider == "frigate")
}
func (h *Hub) addSite(s Site) {
	c := &connection{Site: s, link: &mediaLink{}, channels: []Channel{}}
	c.broker = connectBroker(s.ID, nil, s.Token)
	target := address("http://viewhub-edge")
	c.proxy = &httputil.ReverseProxy{
		Transport: &http.Transport{DialContext: c.link.dial, ResponseHeaderTimeout: 25 * time.Second, DisableKeepAlives: true},
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Header.Del("Cookie")
			p.Out.Header.Set("Authorization", "Bearer "+c.Token)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			failure(w, 502, "现场视频连接不可用")
		},
	}
	h.sites[s.ID] = c
	go c.refresh()
}
func (h *Hub) site(id string) *connection { h.mu.RLock(); defer h.mu.RUnlock(); return h.sites[id] }

func (c *connection) online() bool {
	c.link.mu.Lock()
	defer c.link.mu.Unlock()
	return c.link.session != nil && !c.link.session.IsClosed()
}
func (c *connection) refresh() {
	for {
		if c.online() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			r := httptest.NewRequestWithContext(ctx, "GET", "/api/site/channels", nil)
			w := httptest.NewRecorder()
			c.broker.call(w, r)
			cancel()
			var rows []Channel
			if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &rows) == nil {
				c.mu.Lock()
				c.channels = rows
				c.mu.Unlock()
			}
		}
		time.Sleep(3 * time.Second)
	}
}
func (h *Hub) Inventory(space int64) []Inventory {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := []Inventory{}
	for _, s := range h.sites {
		if s.Space != space {
			continue
		}
		s.mu.Lock()
		out = append(out, Inventory{s.ID, s.Name, s.Provider, s.online(), append([]Channel{}, s.channels...)})
		s.mu.Unlock()
	}
	return out
}
func (h *Hub) Accept(w http.ResponseWriter, r *http.Request) {
	s := h.site(r.PathValue("site"))
	if s == nil {
		http.NotFound(w, r)
		return
	}
	s.link.accept(s.Token)(w, r)
}
func (h *Hub) RecorderSetup(space int64, w http.ResponseWriter, r *http.Request) {
	s := h.site(r.PathValue("site"))
	if s == nil || s.Space != space {
		http.NotFound(w, r)
		return
	}
	if s.Provider != "edge" && r.Method != http.MethodDelete {
		failure(w, 422, "ONVIF 录像机只能通过轻边缘网关接入")
		return
	}
	if !s.online() {
		failure(w, 503, "现场网关离线")
		return
	}
	r = r.Clone(r.Context())
	r.URL.Path, r.URL.RawPath, r.URL.RawQuery = "/api/site/onvif", "", ""
	s.broker.call(w, r)
}

func (h *Hub) Serve(space int64, owner string, w http.ResponseWriter, r *http.Request) {
	s := h.site(r.PathValue("site"))
	if s == nil || s.Space != space {
		http.NotFound(w, r)
		return
	}
	id, operation := r.PathValue("channel"), r.PathValue("operation")
	if !identifier.MatchString(id) || !identifier.MatchString(operation) {
		http.NotFound(w, r)
		return
	}
	tail := r.PathValue("tail")
	if strings.Contains(tail, "..") {
		http.NotFound(w, r)
		return
	}
	path := "/api/site/" + id + "/" + operation
	if tail != "" {
		path += "/" + tail
	}
	switch operation {
	case "recordings", "ptz", "rtc", "relay", "replay", "replay-offer", "replay-control", "vod":
	default:
		http.NotFound(w, r)
		return
	}
	if !s.online() {
		failure(w, 503, "现场网关离线")
		return
	}
	r = r.Clone(r.Context())
	r.URL.Path, r.URL.RawPath = path, ""
	r.RequestURI = r.URL.RequestURI()
	r.Header.Del(replayOwnerHeader)
	r.Header.Del(replayTimeHeader)
	r.Header.Del(replayTicketHeader)
	if operation == "replay" || operation == "replay-control" || operation == "replay-offer" {
		if owner == "" || tail != "" {
			http.NotFound(w, r)
			return
		}
		if operation == "replay" && r.Method == http.MethodGet {
			r.Header.Set(replayOwnerHeader, owner)
		} else if operation != "replay" && r.Method == http.MethodPost {
			if err := signReplayRequest(r, s.Token, owner); err != nil {
				failure(w, 400, "无效回放请求")
				return
			}
			s.proxy.ServeHTTP(w, r)
			return
		}
	}
	if isRPC(r.Method, r.RequestURI) {
		s.broker.call(w, r)
		return
	}
	if r.Method == "GET" && (operation == "rtc" || operation == "relay" || operation == "replay" || operation == "vod") {
		s.proxy.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}
func Run() {
	secret := os.Getenv("VIEWHUB_EDGE_TOKEN")
	if len(secret) < 32 {
		log.Fatal("VIEWHUB_EDGE_TOKEN requires at least 32 characters")
	}
	h := newEdge(secret)
	connectBroker(env("VIEWHUB_ID", "main"), h, secret)
	go connectMedia(h, secret)
	server := &http.Server{Addr: env("VIEWHUB_LISTEN", ":8080"), Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(server.ListenAndServe())
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func address(value string) *url.URL {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		log.Fatal("invalid service origin")
	}
	return u
}
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
func tokenMatches(r *http.Request, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+secret)) == 1
}
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

var unavailable = errors.New("设备接口暂不可用")
