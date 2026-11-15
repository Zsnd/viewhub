package gb

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
	"golang.org/x/net/html/charset"
)

type Config struct {
	PublicIP        string
	SIPPort         int
	ServerID, Realm string
}
type registration struct {
	addr    netip.AddrPort
	expires time.Time
	seen    time.Time
}
type recordRequest struct {
	device, channel string
	events          chan message
}
type Gateway struct {
	cfg      Config
	Store    *Store
	ua       *sipgo.UserAgent
	srv      *sipgo.Server
	client   *sipgo.Client
	dialogs  *sipgo.DialogUA
	active   map[string]*sipgo.DialogClientSession
	contact  sip.ContactHeader
	key      [32]byte
	mu       sync.Mutex
	online   map[string]registration
	attempts map[string]registerAttempt
	catalogs map[string]*catalogAttempt
	record   map[int]recordRequest
	serial   atomic.Uint32
	talks    map[string]*Talk
}

func New(cfg Config, store *Store) (*Gateway, error) {
	ua, err := sipgo.NewUA(sipgo.WithUserAgent("GB28181 Gateway"), sipgo.WithUserAgentHostname(cfg.Realm))
	if err != nil {
		return nil, err
	}
	server, err := sipgo.NewServer(ua)
	if err != nil {
		ua.Close()
		return nil, err
	}
	client, err := sipgo.NewClient(ua, sipgo.WithClientHostname(cfg.PublicIP), sipgo.WithClientPort(cfg.SIPPort),
		sipgo.WithClientConnectionAddr(net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.SIPPort))), sipgo.WithClientNAT())
	if err != nil {
		ua.Close()
		return nil, err
	}
	contact := sip.ContactHeader{Address: sip.Uri{Scheme: "sip", User: cfg.ServerID, Host: cfg.PublicIP, Port: cfg.SIPPort}}
	g := &Gateway{cfg: cfg, Store: store, ua: ua, srv: server, client: client, contact: contact, online: map[string]registration{}, record: map[int]recordRequest{}, attempts: map[string]registerAttempt{}, catalogs: map[string]*catalogAttempt{}}
	if _, err = rand.Read(g.key[:]); err != nil {
		ua.Close()
		return nil, err
	}
	g.dialogs = &sipgo.DialogUA{Client: client, ContactHDR: contact, RewriteContact: true}
	g.active = map[string]*sipgo.DialogClientSession{}
	g.talks = map[string]*Talk{}
	server.OnRegister(g.register)
	server.OnMessage(g.message)
	server.OnInvite(g.talkInvite)
	server.OnAck(g.talkAck)
	server.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) { respond(tx, req, 200, "OK") })
	server.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		if g.talkBye(req, tx) {
			return
		}
		g.mu.Lock()
		var dialog *sipgo.DialogClientSession
		if req.CallID() != nil {
			dialog = g.active[req.CallID().Value()]
		}
		g.mu.Unlock()
		if dialog == nil || dialog.ReadBye(req, tx) != nil {
			respond(tx, req, 481, "Dialog Not Found")
		}
	})
	return g, nil
}
func (g *Gateway) Serve(ctx context.Context) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: g.cfg.SIPPort})
	if err != nil {
		return err
	}
	defer conn.Close()
	tcp, err := net.ListenTCP("tcp4", &net.TCPAddr{Port: g.cfg.SIPPort})
	if err != nil {
		return err
	}
	defer tcp.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close(); tcp.Close(); g.ua.Close() })
	defer stop()
	errs := make(chan error, 2)
	go func() { errs <- g.srv.ServeUDP(conn) }()
	go func() { errs <- g.srv.ServeTCP(tcp) }()
	return <-errs
}
func respond(tx sip.ServerTransaction, req *sip.Request, code int, reason string) {
	_ = tx.Respond(sip.NewResponseFromRequest(req, code, reason, nil))
}

func (g *Gateway) nonce(addr netip.Addr, stamp string) string {
	mac := hmac.New(sha256.New, g.key[:])
	fmt.Fprintf(mac, "%s/%s", stamp, addr)
	// Some cameras use a 64-byte nonce buffer. Keep the full MAC below that limit.
	return stamp + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (g *Gateway) authFailure(req *sip.Request, addr netip.Addr, id, secret string) string {
	auth := req.GetHeader("Authorization")
	if auth == nil {
		return "challenge"
	}
	c, err := digest.ParseCredentials(auth.Value())
	if err != nil || !registerDigestURI(c.URI, req.Recipient) {
		return "invalid_digest"
	}
	if c.Username != id {
		return "username_mismatch"
	}
	if c.Realm != g.cfg.Realm {
		return "realm_mismatch"
	}
	stamp, _, ok := strings.Cut(c.Nonce, ".")
	issued, e := strconv.ParseInt(stamp, 10, 64)
	age := time.Now().Unix() - issued
	if !ok || e != nil || age < 0 || age > 60 || !hmac.Equal([]byte(c.Nonce), []byte(g.nonce(addr, stamp))) {
		return "stale_challenge"
	}
	challenge := &digest.Challenge{Realm: g.cfg.Realm, Nonce: c.Nonce, Algorithm: "MD5"}
	expected, err := digest.Digest(challenge, digest.Options{Method: "REGISTER", URI: c.URI, Username: id, Password: secret})
	if err != nil || subtle.ConstantTimeCompare([]byte(strings.ToLower(c.Response)), []byte(expected.Response)) != 1 {
		return "bad_password"
	}
	return ""
}

func registerDigestURI(value string, recipient sip.Uri) bool {
	if value == recipient.String() {
		return true
	}
	var uri sip.Uri
	if sip.ParseUri(value, &uri) != nil || uri.Scheme != "sip" || recipient.Scheme != "sip" {
		return false
	}
	if uri.Port == 5060 {
		uri.Port = 0
	}
	if recipient.Port == 5060 {
		recipient.Port = 0
	}
	return uri.String() == recipient.String()
}
func (g *Gateway) register(req *sip.Request, tx sip.ServerTransaction) {
	if req.From() == nil || !ValidCode(req.From().Address.User) {
		respond(tx, req, 400, "Invalid Device")
		return
	}
	id := req.From().Address.User
	addr, err := netip.ParseAddrPort(req.Source())
	if err != nil {
		respond(tx, req, 400, "Invalid Source")
		return
	}
	secret, err := g.Store.secret(id)
	failure := "unknown_device"
	if err == nil {
		if req.Recipient.User != "" && req.Recipient.User != g.cfg.ServerID {
			g.rememberRegister(id, "server_id_mismatch")
			respond(tx, req, 404, "SIP Server ID Not Found")
			return
		}
		failure = g.authFailure(req, addr.Addr(), id, secret)
	}
	if failure != "" {
		if err == nil {
			g.rememberRegister(id, failure)
		}
		res := sip.NewResponseFromRequest(req, 401, "Unauthorized", nil)
		nonce := g.nonce(addr.Addr(), strconv.FormatInt(time.Now().Unix(), 10))
		res.AppendHeader(sip.NewHeader("WWW-Authenticate", fmt.Sprintf(`Digest realm="%s",nonce="%s",algorithm=MD5`, g.cfg.Realm, nonce)))
		_ = tx.Respond(res)
		return
	}
	expires := 3600
	if h := req.GetHeader("Expires"); h != nil {
		if n, e := strconv.Atoi(h.Value()); e == nil && n >= 0 {
			expires = min(n, 3600)
		}
	}
	if contact := req.Contact(); contact != nil {
		if v, ok := contact.Params.Get("expires"); ok {
			if n, e := strconv.Atoi(v); e == nil && n >= 0 {
				expires = min(n, 3600)
			}
		}
	}
	now := time.Now()
	g.mu.Lock()
	// Provisioning may have been removed or its password changed while the
	// digest was being checked. Serialize this final check with deletion.
	current, currentErr := g.Store.secret(id)
	if currentErr != nil || current != secret {
		g.mu.Unlock()
		respond(tx, req, 403, "Registration Changed")
		return
	}
	g.attempts[id] = registerAttempt{At: now}
	if expires == 0 {
		delete(g.online, id)
		delete(g.catalogs, id)
	} else {
		g.online[id] = registration{addr, now.Add(time.Duration(expires) * time.Second), now}
	}
	g.mu.Unlock()
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	if c := req.Contact(); c != nil {
		res.AppendHeader(sip.HeaderClone(c))
	}
	res.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(expires)))
	res.AppendHeader(sip.NewHeader("Date", now.UTC().Format("2006-01-02T15:04:05.000")))
	if tx.Respond(res) == nil && expires > 0 {
		slog.Info("GB device registered", "id", id)
		go g.queryCatalog(id)
	}
}
func (g *Gateway) address(id string) (netip.AddrPort, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	state, ok := g.online[id]
	if !ok || time.Now().After(state.expires) || time.Since(state.seen) > 190*time.Second {
		return netip.AddrPort{}, errors.New("device offline")
	}
	return state.addr, nil
}
func (g *Gateway) Devices(space int64) ([]Device, error) {
	all, err := g.Store.devices(space)
	for i := range all {
		all[i].Online, all[i].Diagnostics = g.diagnostic(all[i].ID, time.Now())
	}
	return all, err
}

type Recording struct {
	Name  string `json:"name" xml:"Name"`
	Start string `json:"start" xml:"StartTime"`
	End   string `json:"end" xml:"EndTime"`
}
type message struct {
	Command  string      `xml:"CmdType"`
	ID       string      `xml:"DeviceID"`
	SN       int         `xml:"SN"`
	Total    int         `xml:"SumNum"`
	Channels []Channel   `xml:"DeviceList>Item"`
	Records  []Recording `xml:"RecordList>Item"`
}

func (g *Gateway) message(req *sip.Request, tx sip.ServerTransaction) {
	if req.From() == nil {
		respond(tx, req, 400, "Bad Request")
		return
	}
	id := req.From().Address.User
	source, err := netip.ParseAddrPort(req.Source())
	if err != nil {
		respond(tx, req, 400, "Bad Request")
		return
	}
	g.mu.Lock()
	state, ok := g.online[id]
	tcp := strings.EqualFold(req.Transport(), "TCP")
	if !ok || time.Now().After(state.expires) ||
		(!tcp && state.addr != source) || (tcp && state.addr.Addr() != source.Addr()) {
		g.mu.Unlock()
		respond(tx, req, 403, "Forbidden")
		return
	}
	if !tcp {
		state.seen = time.Now()
		g.online[id] = state
	}
	g.mu.Unlock()
	var msg message
	decoder := xml.NewDecoder(bytes.NewReader(req.Body()))
	decoder.CharsetReader = charset.NewReaderLabel
	if err = decoder.Decode(&msg); err != nil {
		respond(tx, req, 400, "Invalid XML")
		return
	}
	if tcp && msg.Command != "RecordInfo" {
		respond(tx, req, 403, "Forbidden")
		return
	}
	if msg.Command == "RecordInfo" {
		g.mu.Lock()
		target, ok := g.record[msg.SN]
		g.mu.Unlock()
		if !ok || target.device != id || (msg.ID != id && msg.ID != target.channel) {
			respond(tx, req, 403, "Unexpected RecordInfo")
			return
		}
		select {
		case target.events <- msg:
			respond(tx, req, 200, "OK")
		default:
			respond(tx, req, 503, "RecordInfo Busy")
		}
		return
	}
	respond(tx, req, 200, "OK")
	switch msg.Command {
	case "Catalog":
		g.receiveCatalog(id, msg)
	}
}

func (g *Gateway) request(method sip.RequestMethod, target string, address netip.AddrPort, mime string, body []byte) *sip.Request {
	req := sip.NewRequest(method, sip.Uri{Scheme: "sip", User: target, Host: g.cfg.Realm})
	req.SetTransport("UDP")
	req.SetDestination(address.String())
	params := sip.NewParams()
	params.Add("tag", fmt.Sprintf("%x", g.serial.Add(1)))
	req.AppendHeader(&sip.FromHeader{Address: sip.Uri{Scheme: "sip", User: g.cfg.ServerID, Host: g.cfg.Realm}, Params: params})
	req.AppendHeader(sip.HeaderClone(&g.contact))
	req.AppendHeader(sip.NewHeader("Content-Type", mime))
	req.SetBody(body)
	return req
}
func (g *Gateway) sendXML(ctx context.Context, id string, body string) error {
	addr, err := g.address(id)
	if err != nil {
		return err
	}
	req := g.request(sip.MESSAGE, id, addr, "Application/MANSCDP+xml", []byte(body))
	response, err := g.client.Do(ctx, req)
	if err == nil && response.StatusCode != 200 {
		err = fmt.Errorf("SIP MESSAGE %d", response.StatusCode)
	}
	return err
}
func (g *Gateway) Records(ctx context.Context, id, channel string, start, end time.Time) ([]Recording, error) {
	sn := int(g.serial.Add(1))
	events := make(chan message, 32)
	g.mu.Lock()
	g.record[sn] = recordRequest{id, channel, events}
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(g.record, sn); g.mu.Unlock() }()
	layout := "2006-01-02T15:04:05"
	query := fmt.Sprintf("<Query><CmdType>RecordInfo</CmdType><SN>%d</SN><DeviceID>%s</DeviceID><StartTime>%s</StartTime><EndTime>%s</EndTime><Secrecy>0</Secrecy><Type>all</Type></Query>", sn, channel, start.In(time.FixedZone("UTC+8", 8*3600)).Format(layout), end.In(time.FixedZone("UTC+8", 8*3600)).Format(layout))
	if err := g.sendXML(ctx, id, query); err != nil {
		return nil, err
	}
	records := []Recording{}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case msg := <-events:
			records = append(records, msg.Records...)
			if len(records) >= msg.Total {
				return records, nil
			}
		}
	}
}
func (g *Gateway) Address(id string) (netip.AddrPort, error) { return g.address(id) }

func (g *Gateway) Invite(ctx context.Context, addr netip.AddrPort, id, channel string, port int, ssrc string, start, end time.Time) (*sipgo.DialogClientSession, error) {
	session := "Play"
	times := "0 0"
	uri := ""
	if !start.IsZero() {
		session = "Playback"
		times = fmt.Sprintf("%d %d", start.Unix(), end.Unix())
		uri = "u=" + channel + ":0\r\n"
	}
	sdp := fmt.Sprintf("v=0\r\no=%s 0 0 IN IP4 %s\r\ns=%s\r\n%sc=IN IP4 %s\r\nt=%s\r\nm=video %d RTP/AVP 96\r\na=recvonly\r\na=rtpmap:96 PS/90000\r\ny=%s\r\n", g.cfg.ServerID, g.cfg.PublicIP, session, uri, g.cfg.PublicIP, times, port, ssrc)
	req := g.request(sip.INVITE, channel, addr, "application/sdp", []byte(sdp))
	req.AppendHeader(sip.NewHeader("Subject", channel+":"+ssrc+","+g.cfg.ServerID+":0"))
	dialog, err := g.dialogs.WriteInvite(ctx, req)
	if err != nil {
		return nil, err
	}
	if err = dialog.WaitAnswer(ctx, sipgo.AnswerOptions{}); err == nil {
		err = dialog.Ack(ctx)
	}
	if err != nil {
		dialog.Close()
		return nil, err
	}
	callID := req.CallID().Value()
	g.mu.Lock()
	g.active[callID] = dialog
	g.mu.Unlock()
	dialog.OnState(func(state sip.DialogState) {
		if state == sip.DialogStateEnded {
			g.mu.Lock()
			delete(g.active, callID)
			g.mu.Unlock()
		}
	})
	return dialog, nil
}
