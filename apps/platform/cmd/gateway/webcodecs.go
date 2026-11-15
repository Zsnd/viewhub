package main

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func (a *application) webcodecs(w http.ResponseWriter, r *http.Request) {
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || !strings.EqualFold(origin.Host, r.Host) ||
		(origin.Scheme != "https" && (a.secureCookie || origin.Scheme != "http")) {
		problem(w, 403, "请求来源不受信任")
		return
	}
	v, ok := a.sessions.getViewer(r.PathValue("token"), who(r).owner)
	if !ok {
		problem(w, 404, "播放已结束")
		return
	}
	timer := time.NewTimer(7 * time.Second)
	defer timer.Stop()
	select {
	case <-v.feed.stream.Ready:
	case <-v.done:
		problem(w, 404, "播放已结束")
		return
	case <-v.feed.done:
		problem(w, 502, "设备未出画")
		return
	case <-timer.C:
		problem(w, 504, "等待视频超时")
		return
	case <-r.Context().Done():
		return
	}
	config := v.feed.stream.DecoderConfig()
	if config.Codec == "" {
		problem(w, 415, "设备不支持兼容播放")
		return
	}

	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	if conn.SetWriteDeadline(time.Now().Add(time.Second)) != nil || conn.WriteJSON(config) != nil {
		return
	}
	frames, unsubscribe := v.feed.stream.SubscribeFrames()
	defer unsubscribe()

	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		conn.SetReadLimit(1024)
		// Reading processes close frames and detects disconnected browsers.
		_, _, _ = conn.ReadMessage()
	}()
	for {
		select {
		case <-v.done:
			return
		case <-v.feed.done:
			return
		case <-disconnected:
			return
		case frame, open := <-frames:
			if !open || conn.SetWriteDeadline(time.Now().Add(750*time.Millisecond)) != nil ||
				conn.WriteMessage(websocket.BinaryMessage, frame) != nil {
				return
			}
		}
	}
}
