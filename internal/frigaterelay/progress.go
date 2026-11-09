package frigaterelay

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

const ProgressTimeout = 10 * time.Second

type Progress struct {
	Frames uint64 `json:"frames"`
}

// WatchProgress accepts only increasing rendered frame counts from this connection.
func WatchProgress(conn *websocket.Conn, layer string, onProgress func(Progress) error) {
	conn.SetReadLimit(256)
	lastProgress := time.Now()
	conn.SetReadDeadline(lastProgress.Add(ProgressTimeout))
	var lastFrame uint64
	for {
		kind, payload, err := conn.ReadMessage()
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				slog.Info("Frigate relay stopped", "layer", layer, "reason", "no_playback_progress", "idle_ms", time.Since(lastProgress).Milliseconds())
			}
			return
		}
		var progress Progress
		if kind != websocket.TextMessage || json.Unmarshal(payload, &progress) != nil || progress.Frames <= lastFrame {
			continue
		}
		lastFrame = progress.Frames
		lastProgress = time.Now()
		conn.SetReadDeadline(lastProgress.Add(ProgressTimeout))
		if onProgress != nil && onProgress(progress) != nil {
			return
		}
	}
}
