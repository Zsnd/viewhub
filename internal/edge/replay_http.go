package edge

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

func (e *edge) replayOffer(w http.ResponseWriter, r *http.Request) {
	cmd, ok := readReplayTicket(r, e.secret)
	id := r.PathValue("id")
	c, exists := e.getChannel(id)
	if !ok || !exists || !c.hasProfileG() || e.replayRTC == nil || cmd.SDP == "" {
		failure(w, 422, "录像直连不可用")
		return
	}
	s := e.replays.get(r.Header.Get(replayOwnerHeader), id, cmd, true)
	if s == nil {
		failure(w, 409, "回放请求已失效")
		return
	}
	pc, err := e.replayRTC.NewPeerConnection(webrtc.Configuration{ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.cloudflare.com:3478"}}}})
	if err != nil {
		failure(w, 502, "无法建立录像直连")
		return
	}
	accepted := false
	defer func() {
		if !accepted {
			pc.Close()
		}
	}()
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}
	track, err := webrtc.NewTrackLocalStaticSample(codec, "history", "viewhub")
	if err != nil {
		failure(w, 502, "无法创建录像轨道")
		return
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		failure(w, 502, "无法创建录像轨道")
		return
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()
	err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: cmd.SDP})
	if err != nil {
		failure(w, 422, "浏览器未能协商录像视频")
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		failure(w, 422, "浏览器未能协商录像视频")
		return
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if pc.SetLocalDescription(answer) != nil {
		failure(w, 422, "录像协商失败")
		return
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-gathered:
	case <-timer.C:
	case <-r.Context().Done():
		return
	}
	s.mu.Lock()
	if s.check(time.Now()) && s.mode == "" && s.peer == nil {
		s.peer, s.track = pc, track
		accepted = true
	}
	s.mu.Unlock()
	if !accepted {
		failure(w, 409, "回放请求已失效")
		return
	}
	jsonResponse(w, map[string]string{"sdp": pc.LocalDescription().SDP})
}

func (e *edge) replayControl(w http.ResponseWriter, r *http.Request) {
	cmd, ok := readReplayTicket(r, e.secret)
	if !ok {
		failure(w, 403, "播放票无效或已过期")
		return
	}
	id := r.PathValue("id")
	c, exists := e.getChannel(id)
	if !exists || c.usesFrigateVOD() {
		http.NotFound(w, r)
		return
	}
	s := e.replays.get(r.Header.Get(replayOwnerHeader), id, cmd, cmd.Action == "start" || cmd.Action == "stop")
	if s == nil {
		failure(w, 409, "回放会话已结束")
		return
	}
	switch cmd.Action {
	case "stop":
		s.mu.Lock()
		s.stopLocked()
		s.mu.Unlock()
		w.WriteHeader(204)
	case "renew":
		s.mu.Lock()
		renewed := s.renew(cmd, time.Now())
		deadline := s.deadline.UnixMilli()
		s.mu.Unlock()
		if !renewed {
			failure(w, 409, "播放进度无效或会话已到期")
			return
		}
		w.Header().Set("X-ViewHub-Replay-Deadline", strconv.FormatInt(deadline, 10))
		w.WriteHeader(204)
	case "start", "fallback":
		e.beginReplay(w, r, c, s, cmd)
	default:
		failure(w, 400, "未知回放指令")
	}
}

func (e *edge) beginReplay(w http.ResponseWriter, r *http.Request, c channel, s *replaySession, cmd replayCommand) {
	s.mu.Lock()
	fallback := cmd.Action == "fallback"
	valid := s.check(time.Now()) && cmd.At > 0 && (!fallback && s.mode == "" && (cmd.Transport == "mp4" || cmd.Transport == "rtc" && s.peer != nil && (s.peer.ICEConnectionState() == webrtc.ICEConnectionStateConnected || s.peer.ICEConnectionState() == webrtc.ICEConnectionStateCompleted)) ||
		fallback && s.mode == "rtc" && cmd.Transport == "mp4" && cmd.At >= s.at && cmd.At < s.source.end)
	sourceError := s.sourceError
	s.mu.Unlock()
	if !valid {
		failure(w, 409, "回放会话已结束或已切换")
		return
	}
	if sourceError {
		failure(w, 502, "录像机未能返回可播放的录像")
		return
	}
	source, status, message := e.locateReplay(r.Context(), c, cmd.At)
	if status != 0 {
		failure(w, status, message)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	s.mu.Lock()
	if !s.check(time.Now()) || (!fallback && s.mode != "") || (fallback && s.mode != "rtc") {
		s.mu.Unlock()
		failure(w, 409, "回放会话已结束或已切换")
		return
	}
	if s.peer != nil && cmd.Transport == "mp4" {
		go s.peer.Close()
	}
	if s.cancel != nil {
		s.cancel()
	}
	previous := s.previous
	if s.done != nil {
		previous = s.done
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.ctx = ctx
	s.previous = previous
	s.done = nil
	s.mode, s.at, s.source = cmd.Transport, cmd.At, source
	s.position = cmd.At * 1000
	s.lastProgress = time.Now()
	s.frames = 0
	if !fallback {
		s.arm(replayLifetime)
	}
	track := s.track
	if cmd.Transport == "rtc" {
		done := make(chan struct{})
		s.done = done
		go func() {
			defer close(done)
			if !waitReplaySource(ctx, previous) {
				return
			}
			var prior int64
			duration := time.Second / 30
			err := rtspReplay(ctx, source, func(_ int64, dts int64, au [][]byte) {
				if prior != 0 && dts > prior {
					duration = time.Duration(dts-prior) * time.Second / 90000
				}
				prior = dts
				var packet []byte
				for _, nalu := range au {
					packet = append(packet, 0, 0, 0, 1)
					packet = append(packet, nalu...)
				}
				_ = track.WriteSample(media.Sample{Data: packet, Duration: duration})
			})
			if err != nil && ctx.Err() == nil {
				s.mu.Lock()
				if s.ctx == ctx {
					s.sourceError = true
				}
				s.mu.Unlock()
			}
		}()
	}
	deadline := s.deadline.UnixMilli()
	s.mu.Unlock()
	w.Header().Set("X-ViewHub-Replay-Deadline", strconv.FormatInt(deadline, 10))
	w.WriteHeader(204)
}

func (e *edge) replay(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rev, err := strconv.ParseInt(q.Get("revision"), 10, 64)
	slot := q.Get("slot")
	if err != nil || !identifier.MatchString(slot) {
		failure(w, 403, "回放需要有效播放票")
		return
	}
	s := e.replays.get(r.Header.Get(replayOwnerHeader), r.PathValue("id"), replayCommand{Slot: slot, Revision: rev}, false)
	if s == nil {
		failure(w, 403, "回放会话已结束")
		return
	}
	s.mu.Lock()
	if !s.check(time.Now()) || s.mode != "mp4" || s.done != nil {
		s.mu.Unlock()
		failure(w, 409, "回放会话已结束或正在使用")
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	s.done = done
	previous, source := s.previous, s.source
	s.mu.Unlock()
	defer close(done)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	if !waitReplaySource(ctx, previous) {
		return
	}
	sendReplayMP4(ctx, source, w)
}
