package edge

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/hashicorp/yamux"
	ws "nhooyr.io/websocket"
)

type rpcRequest struct {
	Method   string
	Path     string
	Body     []byte
	Deadline int64
}
type rpcReply struct {
	Status int
	Body   []byte
}

type broker struct {
	conn    *autopaho.ConnectionManager
	request string
	reply   string
	pending sync.Map
}

func connectBroker(site string, edge http.Handler, token string) *broker {
	u, err := url.Parse(os.Getenv("VIEWHUB_MQTT_URL"))
	if err != nil || u.Host == "" {
		log.Fatal("configure VIEWHUB_MQTT_URL")
	}

	if !identifier.MatchString(site) {
		log.Fatal("invalid VIEWHUB_ID")
	}
	prefix := "viewhub/" + site
	b := &broker{request: prefix + "/request", reply: prefix + "/reply"}
	name, sub := "cloud", b.reply
	if edge != nil {
		name, sub = "edge", b.request
	}
	config := autopaho.ClientConfig{
		ServerUrls: []*url.URL{u}, KeepAlive: 25, CleanStartOnInitialConnection: true,
		ConnectUsername: os.Getenv("VIEWHUB_MQTT_USER"), ConnectPassword: []byte(os.Getenv("VIEWHUB_MQTT_PASSWORD")),
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ack, err := cm.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: sub, QoS: 1}}})
			if err != nil || len(ack.Reasons) != 1 || ack.Reasons[0] >= 128 {
				log.Print("MQTT subscription failed")
				return
			}
			log.Printf("MQTT %s subscribed", name)
		},
		OnConnectError: func(error) { log.Print("MQTT connection unavailable") },
		ClientConfig: paho.ClientConfig{ClientID: prefix + "/" + name,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(received paho.PublishReceived) (bool, error) {
				p := received.Packet
				if p.Topic != sub || p.Retain || p.Properties == nil || len(p.Properties.CorrelationData) != 32 {
					return false, nil
				}
				id := string(p.Properties.CorrelationData)
				if edge == nil {
					ch, ok := b.pending.Load(id)
					if ok && len(p.Payload) <= 1<<20 {
						select {
						case ch.(chan []byte) <- p.Payload:
						default:
						}
					}
				} else if len(p.Payload) <= 2*recorderRequestLimit {
					go executeRPC(received.Client, payloadCopy(p.Payload), id, b.reply, token, edge)
				}
				return true, nil
			}},
		},
	}
	b.conn, err = autopaho.NewConnection(context.Background(), config)
	if err != nil {
		log.Fatal(err)
	}
	return b
}

func payloadCopy(p []byte) []byte { return append([]byte(nil), p...) }

func executeRPC(client *paho.Client, data []byte, id, reply, token string, edge http.Handler) {
	var req rpcRequest
	if json.Unmarshal(data, &req) != nil || !isRPC(req.Method, req.Path) {
		return
	}
	deadline := time.Unix(req.Deadline, 0)
	if !deadline.After(time.Now()) || time.Until(deadline) > 30*time.Second {
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, req.Method, req.Path, bytes.NewReader(req.Body))
	if err != nil {
		return
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	edge.ServeHTTP(w, r)
	if w.Body.Len() > 1<<20 {
		return
	}
	body, _ := json.Marshal(rpcReply{Status: w.Code, Body: w.Body.Bytes()})
	// Commands are not replayed after reconnect. In particular, a stale PTZ move must not execute.
	client.Publish(ctx, &paho.Publish{Topic: reply, Payload: body, Properties: &paho.PublishProperties{CorrelationData: []byte(id)}})
}

func isRPC(method, path string) bool {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() {
		return false
	}
	if u.Path == "/api/site/onvif" {
		return u.RawQuery == "" && (method == "POST" || method == "PUT" || method == "DELETE")
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) == 4 && u.Path == "/api/site/channels" {
		return method == "GET"
	}
	if len(parts) != 5 || parts[1] != "api" || parts[2] != "site" || !identifier.MatchString(parts[3]) {
		return false
	}
	return (method == "GET" && parts[4] == "recordings") || (method == "POST" && parts[4] == "ptz")
}

func (b *broker) call(w http.ResponseWriter, r *http.Request) {
	limit := int64(4096)
	if r.URL.Path == "/api/site/onvif" {
		limit = recorderRequestLimit
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		failure(w, 400, "请求内容无效")
		return
	}
	timeout := 20 * time.Second
	if r.URL.Path == "/api/site/onvif" {
		timeout = 25 * time.Second
	} else if r.Method == "POST" {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	if b.conn.AwaitConnection(ctx) != nil {
		failure(w, 503, "现场消息连接不可用")
		return
	}
	deadline, _ := ctx.Deadline()
	payload, _ := json.Marshal(rpcRequest{Method: r.Method, Path: r.URL.RequestURI(), Body: body, Deadline: deadline.Unix()})
	id := hex.EncodeToString(randomBytes(16))
	result := make(chan []byte, 1)
	b.pending.Store(id, result)
	defer b.pending.Delete(id)
	_, err = b.conn.Publish(ctx, &paho.Publish{Topic: b.request, Payload: payload, Properties: &paho.PublishProperties{CorrelationData: []byte(id), ResponseTopic: b.reply}})
	if err == nil {
		select {
		case raw := <-result:
			var answer rpcReply
			if json.Unmarshal(raw, &answer) == nil && answer.Status >= 200 && answer.Status <= 599 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(answer.Status)
				w.Write(answer.Body)
				return
			}
		case <-ctx.Done():
		}
	}
	failure(w, 504, "现场服务未响应")
}

type mediaLink struct {
	mu      sync.Mutex
	session *yamux.Session
}

func (link *mediaLink) accept(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenMatches(r, token) {
			http.Error(w, "unauthorized", 401)
			return
		}
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		stream := ws.NetConn(r.Context(), conn, ws.MessageBinary)
		session, err := yamux.Client(stream, nil)
		if err != nil {
			return
		}
		defer session.Close()
		link.mu.Lock()
		if link.session != nil {
			link.session.Close()
		}
		link.session = session
		link.mu.Unlock()
		<-session.CloseChan()
		link.mu.Lock()
		if link.session == session {
			link.session = nil
		}
		link.mu.Unlock()
	}
}

func (link *mediaLink) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	link.mu.Lock()
	session := link.session
	link.mu.Unlock()
	if session == nil {
		return nil, errors.New("edge media link offline")
	}
	return session.OpenStream()
}

func connectMedia(edge http.Handler, token string) {
	target := os.Getenv("VIEWHUB_MEDIA_URL")
	if target == "" {
		log.Fatal("configure VIEWHUB_MEDIA_URL")
	}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		conn, _, err := ws.Dial(ctx, target, &ws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
		cancel()
		if err == nil {
			session, err := yamux.Server(ws.NetConn(context.Background(), conn, ws.MessageBinary), nil)
			if err == nil {
				log.Print("edge media link connected")
				server := &http.Server{Handler: edge, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 20 * time.Second}
				server.Serve(session)
				session.Close()
			}
			conn.CloseNow()
		}
		time.Sleep(2 * time.Second)
	}
}
