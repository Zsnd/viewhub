package edge

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
)

func (e *edge) setupCamera(w http.ResponseWriter, r *http.Request, c onvifConfig, input recorderInput) {
	target, profiles, err := c.mediaProfiles(r.Context())
	if err != nil || len(profiles) == 0 {
		failure(w, 422, "摄像头未提供可用的 ONVIF 实时配置")
		return
	}
	if r.Method == http.MethodPost {
		type capability struct {
			Token string `json:"token"`
			Name  string `json:"name"`
			PTZ   bool   `json:"ptz"`
		}
		choices := make([]capability, 0, len(profiles))
		for _, profile := range profiles {
			choices = append(choices, capability{profile.Token, profile.Name, profile.PTZ != nil && profile.PTZ.Token != ""})
		}
		jsonResponse(w, choices)
		return
	}
	name := strings.TrimSpace(input.RecorderName)
	if name == "" || len(name) > 180 {
		failure(w, 400, "请填写摄像头名称")
		return
	}
	for _, profile := range profiles {
		if profile.Token != input.LiveProfile {
			continue
		}
		stream, err := c.streamURI(r.Context(), target, profile.Token)
		if err != nil {
			failure(w, 422, "摄像头未返回可用的实时地址")
			return
		}
		c.Recording, c.Track, c.Profile = "", "", profile.Token
		if profile.PTZ != nil && profile.PTZ.Token != "" {
			_, err := c.service(r.Context(), ptzNS)
			c.PTZ = err == nil
		}
		key := sha256.Sum256([]byte(c.Device))
		id := fmt.Sprintf("camera-%x", key[:12])
		if err = e.recorders.write(id, &recorderBinding{Name: name, RecorderName: name, ONVIF: c, LiveURI: stream, Kind: "camera"}); err != nil {
			failure(w, 500, "无法保存摄像头")
			return
		}
		jsonResponse(w, map[string]int{"added": 1})
		return
	}
	failure(w, 422, "请重新检测并选择可用的实时视频")
}
