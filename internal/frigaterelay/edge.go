package frigaterelay

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// Edge opens one Frigate consumer per cloud connection and owns its timeout.
func Edge(frigate *url.URL, camera string) http.Handler {
	target := *frigate
	target.Scheme, target.Path = "ws", "/live/mse/api/ws"
	target.RawQuery = url.Values{"src": {camera}}.Encode()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /live", func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			http.Error(w, "WebSocket required", http.StatusBadRequest)
			return
		}
		dialer := websocket.Dialer{HandshakeTimeout: 4 * time.Second}
		media, res, err := dialer.DialContext(r.Context(), target.String(), nil)
		if err != nil {
			if res != nil {
				res.Body.Close()
			}
			http.Error(w, "Frigate unavailable", http.StatusBadGateway)
			return
		}
		defer media.Close()
		upgrader := websocket.Upgrader{}
		cloud, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer cloud.Close()

		go func() {
			defer cloud.Close()
			defer media.Close()
			WatchProgress(cloud, "edge", nil)
		}()
		media.SetWriteDeadline(time.Now().Add(4 * time.Second))
		if err := media.WriteJSON(map[string]string{"type": "mse", "value": "avc1.640029,avc1.4d0029,avc1.420029,mp4a.40.2"}); err != nil {
			return
		}
		for {
			media.SetReadDeadline(time.Now().Add(20 * time.Second))
			kind, data, err := media.ReadMessage()
			if err != nil {
				return
			}
			cloud.SetWriteDeadline(time.Now().Add(8 * time.Second))
			if cloud.WriteMessage(kind, data) != nil {
				return
			}
		}
	})
	return mux
}
