package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"
	"viewhub/internal/edge"
	"viewhub/internal/gb"
)

const version = "0.4.2"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func port(name string, def int) int {
	v, err := strconv.Atoi(env(name, strconv.Itoa(def)))
	if err != nil || v < 1 || v > 65535 {
		panic("invalid " + name)
	}
	return v
}
func main() {
	if err := run(); err != nil {
		slog.Error("gateway", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ip, err := netip.ParseAddr(env("PUBLIC_IP", "127.0.0.1"))
	if err != nil || !ip.Is4() {
		return errors.New("PUBLIC_IP must be an IPv4 address")
	}
	serverID := env("SIP_ID", "34020000002000000002")
	realm := env("SIP_REALM", "3402000000")
	if !gb.ValidCode(serverID) || len(realm) != 10 || !strings.HasPrefix(serverID, realm) {
		return errors.New("invalid SIP identity")
	}
	store, err := gb.OpenStore(env("DB_PATH", "gateway.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	if id := os.Getenv("DEVICE_ID"); id != "" {
		secret, err := os.ReadFile(env("DEVICE_SECRET_FILE", "device.secret"))
		if err != nil {
			return err
		}
		if err = store.SeedDevice(id, env("DEVICE_NAME", "海康枪机"), env("DEVICE_KIND", "camera"), strings.TrimSpace(string(secret))); err != nil {
			return err
		}
	}
	direct, err := loadDirect(env("MIBEE_DIRECT_CONFIG", "/secrets/mibee-direct.json"))
	if err != nil {
		return err
	}
	var mibeeDevice string
	if direct != nil {
		mibeeDevice = direct.Device
	}
	gateway, err := gb.New(gb.Config{PublicIP: ip.String(), SIPPort: port("SIP_PORT", 5060), ServerID: serverID, Realm: realm, UntaggedDevice: mibeeDevice}, store)
	if err != nil {
		return err
	}
	first, last := port("RTP_FIRST", 10000), port("RTP_LAST", 10099)
	if first > last {
		return errors.New("invalid RTP range")
	}
	manager := &sessions{gb: gateway, first: first, last: last, feeds: map[string]*feed{}, viewers: map[string]viewer{}}
	go manager.reap(ctx)
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port("ICE_PORT", 8191)})
	if err != nil {
		return err
	}
	defer udp.Close()
	settings := webrtc.SettingEngine{}
	settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	settings.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udp))
	settings.SetNAT1To1IPs([]string{ip.String()}, webrtc.ICECandidateTypeHost)
	rtc := webrtc.NewAPI(webrtc.WithSettingEngine(settings))
	password, err := os.ReadFile(env("WEB_SECRET_FILE", "web.secret"))
	if err != nil {
		return err
	}
	if err = store.EnsureAdmin(env("WEB_USER", "viewer"), strings.TrimSpace(string(password))); err != nil {
		return err
	}
	store.PurgeSessions()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				store.PurgeSessions()
			}
		}
	}()
	var frigate *frigateSource
	if origin := os.Getenv("FRIGATE_ORIGIN"); origin != "" {
		frigate, err = newFrigate(origin, env("FRIGATE_CAMERA", "hik_bullet"))
		if err != nil {
			return err
		}
		if relay := os.Getenv("FRIGATE_RELAY_ORIGIN"); relay != "" {
			frigate.relayOrigin, err = frigateURL(relay)
			if err != nil {
				return err
			}
		}
	}
	edgeHub, err := edge.Open(env("VIEWHUB_SITES_FILE", "/secrets/sites.json"), os.Getenv("VIEWHUB_PROVISION_DIR"))
	if err != nil {
		return err
	}
	app := &application{edgeHub: edgeHub, frigate: frigate, gateway: gateway, sessions: manager, rtc: rtc, sipAddress: ip.String(), serverID: serverID, realm: realm, sipPort: port("SIP_PORT", 5060), secureCookie: env("COOKIE_SECURE", "true") != "false", direct: direct}
	server := &http.Server{Addr: env("HTTP_ADDR", ":8080"), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 45 * time.Second, Handler: app.protect(app.routes())}
	errorsOut := make(chan error, 2)
	go func() { errorsOut <- gateway.Serve(ctx) }()
	go func() { errorsOut <- server.ListenAndServe() }()
	slog.Info("gateway started", "version", version, "sip", app.sipPort, "ice", udp.LocalAddr())
	select {
	case <-ctx.Done():
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(deadline)
	case err := <-errorsOut:
		return err
	}
}

type application struct {
	edgeHub                     *edge.Hub
	frigate                     *frigateSource
	direct                      *directBridge
	gateway                     *gb.Gateway
	sessions                    *sessions
	rtc                         *webrtc.API
	serverID, realm, sipAddress string
	sipPort                     int
	secureCookie                bool
}

func jsonReply(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func problem(w http.ResponseWriter, status int, reason string) {
	jsonReply(w, status, map[string]string{"error": reason})
}

func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	a.viewHubRoutes(mux)
	if a.direct != nil {
		mux.HandleFunc("GET /edge/mibee", a.direct.edge)
		mux.HandleFunc("POST /edge/mibee/{job}", a.direct.edge)
	}
	mux.HandleFunc("POST /api/devices/{id}/{channel}/direct", a.directPlayback)
	mux.HandleFunc("DELETE /api/devices/{id}/{channel}/direct/{session}", a.directPlayback)
	mux.HandleFunc("GET "+frigatePath+"/live", a.frigateLive)
	mux.HandleFunc("GET "+frigatePath+"/rtc", a.frigateRTC)
	mux.HandleFunc("GET "+frigatePath+"/vod/{start}/{end}/{asset}", a.frigateVOD)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	mux.HandleFunc("POST /api/auth/password", a.password)
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) { jsonReply(w, 200, who(r).account) })
	mux.HandleFunc("GET /api/admin/accounts", a.accounts)
	mux.HandleFunc("POST /api/admin/accounts", a.createAccount)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]string{"version": version, "status": "ok"})
	})
	mux.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) {
		list, err := a.gateway.Devices(who(r).account.SpaceID)
		if err != nil {
			problem(w, 500, "读取设备失败")
			return
		}
		if a.frigateAllowed(r) {
			list = append([]gb.Device{a.frigate.device(r.Context())}, list...)
		}
		inventory := map[string]any{"devices": list, "sipAddress": a.sipAddress, "sipId": a.serverID, "sipRealm": a.realm, "sipPort": a.sipPort}
		if d := a.direct; d != nil && a.gateway.Store.HasChannel(who(r).account.SpaceID, d.Device, d.Channel) {
			inventory["directChannel"] = d.Channel
			inventory["directDevice"] = d.Device
		}
		jsonReply(w, 200, inventory)
	})
	mux.HandleFunc("POST /api/devices", func(w http.ResponseWriter, r *http.Request) {
		var data struct{ ID, Name, Kind, Secret string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&data) != nil {
			problem(w, 400, "请填写有效的设备编码、名称和注册密码")
			return
		}
		err := a.gateway.Store.Provision(who(r).account.SpaceID, data.ID, data.Name, data.Kind, data.Secret)
		if errors.Is(err, gb.ErrDeviceExists) {
			problem(w, 409, "设备编码已被使用，请填写其他国标编码")
			return
		}
		if err != nil {
			problem(w, 400, "请填写有效的设备编码、名称和注册密码")
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/devices/{id}/setup", a.deviceSetup)
	mux.HandleFunc("GET /api/devices/{id}/{channel}/records", a.records)
	mux.HandleFunc("POST /api/devices/{id}/{channel}/play", a.play)
	mux.HandleFunc("POST /api/session/{token}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Progressing bool }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body) != nil {
			problem(w, 400, "续约参数无效")
			return
		}
		if !a.sessions.renew(r.PathValue("token"), who(r).owner, body.Progressing, time.Now()) {
			problem(w, 404, "播放已结束")
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("DELETE /api/session/{token}", func(w http.ResponseWriter, r *http.Request) {
		if a.sessions.getWithoutRenew(r.PathValue("token"), who(r).owner) == nil {
			problem(w, 404, "播放已结束")
			return
		}
		if done := a.sessions.stop(r.PathValue("token"), who(r).owner, "viewer_release"); done != nil {
			timer := time.NewTimer(4 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
			case <-r.Context().Done():
			}
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/session/{token}/whep", a.whep)
	mux.HandleFunc("GET /api/session/{token}/webcodecs", a.webcodecs)
	mux.HandleFunc("GET /api/session/{token}/hls/{asset}", func(w http.ResponseWriter, r *http.Request) {
		f := a.sessions.getWithoutRenew(r.PathValue("token"), who(r).owner)
		if f == nil {
			http.NotFound(w, r)
			return
		}
		timer := time.NewTimer(9 * time.Second)
		defer timer.Stop()
		select {
		case <-f.stream.Ready:
		case <-f.done:
			problem(w, 502, "设备未出画")
			return
		case <-timer.C:
			problem(w, 504, "等待视频超时")
			return
		case <-r.Context().Done():
			return
		}
		r = r.Clone(r.Context())
		r.URL.Path = "/" + r.PathValue("asset")
		r.URL.RawPath = ""
		f.stream.ServeHLS(w, r)
	})
	mux.Handle("/", http.FileServer(http.Dir(env("WEB_ROOT", "web/dist"))))
	return mux
}

func parseWindow(start, end string) (time.Time, time.Time, error) {
	x, e1 := time.Parse(time.RFC3339, start)
	y, e2 := time.Parse(time.RFC3339, end)
	if e1 != nil || e2 != nil || !y.After(x) || y.Sub(x) > 48*time.Hour {
		return time.Time{}, time.Time{}, errors.New("invalid time range")
	}
	return x, y, nil
}
func (a *application) records(w http.ResponseWriter, r *http.Request) {
	id, ch := r.PathValue("id"), r.PathValue("channel")
	start, end, err := parseWindow(r.URL.Query().Get("start"), r.URL.Query().Get("end"))
	if id == frigateDevice {
		if !a.frigateAllowed(r) || ch != a.frigate.camera {
			problem(w, 404, "未找到设备通道")
		} else if err != nil {
			problem(w, 400, "录像通道或时间范围无效")
		} else {
			a.frigateRecords(w, r, start, end)
		}
		return
	}
	if !a.gateway.Store.HasChannel(who(r).account.SpaceID, id, ch) {
		problem(w, 404, "未找到设备通道")
		return
	}
	if err != nil {
		problem(w, 400, "录像通道或时间范围无效")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	list, err := a.gateway.Records(ctx, id, ch, start, end)
	if err != nil {
		problem(w, 502, "录像机未返回录像目录")
		return
	}
	jsonReply(w, 200, list)
}
func (a *application) play(w http.ResponseWriter, r *http.Request) {
	id, ch := r.PathValue("id"), r.PathValue("channel")
	actor := who(r)
	if id == frigateDevice && (!a.frigateAllowed(r) || ch != a.frigate.camera) ||
		id != frigateDevice && !a.gateway.Store.HasChannel(actor.account.SpaceID, id, ch) {
		problem(w, 404, "未找到设备通道")
		return
	}
	var body struct{ Start, End string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&body); err != nil && err != io.EOF {
		problem(w, 400, "播放参数无效")
		return
	}
	var start, end time.Time
	if body.Start != "" || body.End != "" {
		var err error
		start, end, err = parseWindow(body.Start, body.End)
		if err != nil {
			problem(w, 400, "回放时间范围无效")
			return
		}
	}
	if id == frigateDevice {
		if start.IsZero() {
			jsonReply(w, 201, map[string]string{"id": "frigate-live", "source": "frigate", "live": frigatePath + "/live", "rtc": frigatePath + "/rtc"})
		} else if end.Sub(start) > time.Hour {
			problem(w, 400, "单次 Frigate 回放最长一小时")
		} else {
			path := fmt.Sprintf("%s/vod/%d/%d/index.m3u8", frigatePath, start.Unix(), end.Unix())
			jsonReply(w, 201, map[string]string{"id": "frigate-recording", "source": "frigate", "hls": path})
		}
		return
	}
	session, err := a.sessions.start(r.Context(), actor, id, ch, start, end)
	if err != nil {
		problem(w, 503, "设备离线或暂时无法播放")
		return
	}
	path := "/api/session/" + session
	jsonReply(w, 201, map[string]string{"id": session, "rtc": path + "/whep", "webcodecs": path + "/webcodecs", "hls": path + "/hls/index.m3u8"})
}
func (a *application) whep(w http.ResponseWriter, r *http.Request) {
	f := a.sessions.getWithoutRenew(r.PathValue("token"), who(r).owner)
	if f == nil {
		problem(w, 404, "播放已结束")
		return
	}
	offer, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		problem(w, 400, "invalid SDP")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
	defer cancel()
	select {
	case <-f.stream.Ready:
	case <-f.done:
		problem(w, 502, "设备未出画")
		return
	case <-ctx.Done():
		problem(w, 504, "等待视频超时")
		return
	}
	pc, err := a.rtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		problem(w, 500, "WebRTC unavailable")
		return
	}
	ok := false
	defer func() {
		if !ok {
			pc.Close()
		}
	}()
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}); err != nil {
		problem(w, 400, "invalid SDP")
		return
	}
	track := f.stream.Track
	sender, err := pc.AddTrack(track)
	if err != nil {
		problem(w, 400, "no compatible video codec")
		return
	}
	if track.Codec().MimeType == webrtc.MimeTypeH265 &&
		!slices.ContainsFunc(sender.GetParameters().Codecs, func(codec webrtc.RTPCodecParameters) bool {
			return strings.EqualFold(codec.MimeType, track.Codec().MimeType)
		}) {
		problem(w, 415, "浏览器未协商 H.265 WebRTC")
		return
	}
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buffer); err != nil {
				return
			}
		}
	}()
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		problem(w, 500, "cannot answer SDP")
		return
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		problem(w, 500, "cannot set SDP")
		return
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		problem(w, 504, "ICE timeout")
		return
	}
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed {
			pc.Close()
		}
	})
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-f.done:
				pc.Close()
				return
			case <-ticker.C:
				if pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
					return
				}
				if a.sessions.getWithoutRenew(r.PathValue("token"), who(r).owner) == nil {
					pc.Close()
					return
				}
			}
		}
	}()
	w.Header().Set("Content-Type", "application/sdp")
	w.WriteHeader(201)
	_, _ = fmt.Fprint(w, pc.LocalDescription().SDP)
	ok = true
}

func (a *application) deviceSetup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	secret, err := a.gateway.Store.SetupSecret(who(r).account.SpaceID, id)
	if errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "设备不存在")
		return
	}
	if err != nil {
		problem(w, 500, "读取接入配置失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonReply(w, 200, map[string]any{
		"deviceId": id, "sipAddress": a.sipAddress, "sipPort": a.sipPort,
		"sipId": a.serverID, "sipRealm": a.realm, "secret": secret,
		"protocol": "GB/T 28181-2016", "transport": "UDP",
	})
}
