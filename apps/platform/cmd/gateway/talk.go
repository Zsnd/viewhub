package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
	"viewhub/apps/platform/gb"
)

type talkViewer struct {
	owner, device, channel string
	accountID              int64
	pc                     *webrtc.PeerConnection
	call                   *gb.Talk
	playback               string
	expires                atomic.Int64
}

func (a *application) talkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/devices/{id}/{channel}/talk", a.startTalk)
	mux.HandleFunc("POST /api/talk/{token}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		v := a.ownedTalk(r.PathValue("token"), who(r).owner)
		if v == nil || !a.gateway.Store.HasChannel(who(r).account.SpaceID, v.device, v.channel) {
			problem(w, 404, "对讲已结束")
			return
		}
		v.expires.Store(time.Now().Add(15 * time.Second).UnixNano())
		if !a.sessions.renew(v.playback, v.owner, true, time.Now()) {
			v.call.Close()
			problem(w, 404, "现场音频已结束，请重新连接")
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("DELETE /api/talk/{token}", func(w http.ResponseWriter, r *http.Request) {
		v := a.ownedTalk(r.PathValue("token"), who(r).owner)
		if v == nil {
			problem(w, 404, "对讲已结束")
			return
		}
		v.call.Close()
		v.pc.Close()
		w.WriteHeader(204)
	})
}
func (a *application) ownedTalk(id, owner string) *talkViewer {
	value, ok := a.talks.Load(id)
	if !ok {
		return nil
	}
	v := value.(*talkViewer)
	if v.owner != owner || time.Now().UnixNano() > v.expires.Load() {
		return nil
	}
	select {
	case <-v.call.Done:
		return nil
	default:
		return v
	}
}

func (a *application) startTalk(w http.ResponseWriter, r *http.Request) {
	actor := who(r)
	device, channel := r.PathValue("id"), r.PathValue("channel")
	if !a.gateway.Store.HasChannel(actor.account.SpaceID, device, channel) {
		problem(w, 404, "未找到设备通道")
		return
	}
	offer, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || len(offer) == 0 {
		problem(w, 400, "对讲协商参数无效")
		return
	}
	pc, err := a.rtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		problem(w, 500, "暂时无法创建音频连接")
		return
	}
	keep := false
	defer func() {
		if !keep {
			pc.Close()
		}
	}()
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}); err != nil {
		problem(w, 400, "浏览器音频协商失败")
		return
	}
	compatible := false
	for _, transceiver := range pc.GetTransceivers() {
		if transceiver.Kind() != webrtc.RTPCodecTypeAudio {
			continue
		}
		if err = transceiver.SetCodecPreferences([]webrtc.RTPCodecParameters{{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMA, ClockRate: 8000, Channels: 1}, PayloadType: 8}}); err == nil {
			compatible = true
		}
	}
	if !compatible {
		problem(w, 415, "浏览器未提供 G.711A 音频，请使用支持该编码的浏览器")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	call, err := a.gateway.BeginTalk(ctx, actor.account.SpaceID, device, channel, a.sessions.first, a.sessions.last)
	if errors.Is(err, gb.ErrTalkBusy) {
		problem(w, 409, "此设备正在对讲，请结束现有会话后重试")
		return
	}
	if err != nil {
		problem(w, 502, "设备未建立语音连接，请检查对讲能力和媒体 TCP 端口")
		return
	}
	defer func() {
		if !keep {
			call.Close()
		}
	}()
	// A logout, password change, or deletion during SIP negotiation cancels admission.
	if _, err = a.gateway.Store.Session(actor.owner); err != nil || !a.gateway.Store.HasChannel(actor.account.SpaceID, device, channel) {
		problem(w, 401, "登录或设备状态已变化，请重新操作")
		return
	}
	// Reuse the live feed for the return audio. It stays independent of the
	// browser's video codec/fallback, so HEVC video can still have PCMA talk.
	playback, err := a.sessions.start(ctx, actor, device, channel, time.Time{}, time.Time{})
	if err != nil {
		problem(w, 502, "无法连接现场音频，请检查设备实时码流")
		return
	}
	defer func() {
		if !keep {
			a.sessions.stop(playback, actor.owner, "talk_failed")
		}
	}()
	feed := a.sessions.getWithoutRenew(playback, actor.owner)
	if feed == nil || feed.stream.AudioTrack == nil {
		problem(w, 502, "现场音频会话未建立")
		return
	}
	audioSender, err := pc.AddTrack(feed.stream.AudioTrack)
	if err != nil {
		problem(w, 415, "浏览器未能协商现场声音")
		return
	}
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, err := audioSender.Read(buffer); err != nil {
				return
			}
		}
	}()
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if !strings.EqualFold(track.Codec().MimeType, webrtc.MimeTypePCMA) {
			call.Close()
			pc.Close()
			return
		}
		for {
			packet, _, err := track.ReadRTP()
			if err != nil {
				call.Close()
				return
			}
			if err = call.WriteAudio(packet.Payload); err != nil {
				call.Close()
				pc.Close()
				return
			}
		}
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			call.Close()
		}
	})
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		problem(w, 415, "无法协商 G.711A 音频")
		return
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		problem(w, 500, "无法建立浏览器音频连接")
		return
	}
	select {
	case <-gathered:
	case <-call.Done:
		problem(w, 502, "设备已结束对讲")
		return
	case <-ctx.Done():
		problem(w, 504, "音频连接超时")
		return
	}
	id := token()
	v := &talkViewer{owner: actor.owner, accountID: actor.account.ID, device: device, channel: channel, pc: pc, call: call, playback: playback}
	v.expires.Store(time.Now().Add(15 * time.Second).UnixNano())
	a.talks.Store(id, v)
	go func() {
		defer a.talks.Delete(id)
		defer pc.Close()
		defer call.Close()
		defer a.sessions.stop(playback, actor.owner, "talk_ended")
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-call.Done:
				return
			case <-ticker.C:
				if time.Now().UnixNano() > v.expires.Load() {
					return
				}
			}
		}
	}()
	jsonReply(w, 201, map[string]string{"id": id, "sdp": pc.LocalDescription().SDP, "mode": "duplex", "codec": "PCMA"})
	keep = true
}

func (a *application) stopTalks(owner string, accountID int64, device string) {
	a.talks.Range(func(_, value any) bool {
		v := value.(*talkViewer)
		if (owner == "" && accountID == 0 && device == "") || (owner != "" && v.owner == owner) || (accountID != 0 && v.accountID == accountID) || (device != "" && v.device == device) {
			v.call.Close()
			v.pc.Close()
		}
		return true
	})
}
