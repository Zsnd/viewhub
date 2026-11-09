package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"viewhub/internal/gb"
)

const edgeReleaseVersion = "0.4.2"

func (a *application) viewHubRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/devices/{id}", a.updateDevice)
	if a.edgeHub != nil {
		mux.HandleFunc("GET /edge/media/{site}", a.edgeHub.Accept)
		mux.HandleFunc("POST /api/edge", func(w http.ResponseWriter, r *http.Request) {
			a.edgeHub.Provision(who(r).account.SpaceID, env("VIEWHUB_PUBLIC_ORIGIN", "https://viewhub.xliyun.com"), edgeReleaseVersion, w, r)
		})
		mux.HandleFunc("GET /edge-release/{version}/{binary}", func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("binary")
			if (r.PathValue("version") != edgeReleaseVersion && r.PathValue("version") != "0.4.0") || (name != "viewhub-edge-amd64" && name != "viewhub-edge-arm64") {
				http.NotFound(w, r)
				return
			}
			root := os.Getenv("VIEWHUB_RELEASE_DIR")
			if root == "" {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, root+"/"+name)
		})
		mux.HandleFunc("GET /api/edge", func(w http.ResponseWriter, r *http.Request) {
			jsonReply(w, 200, a.edgeHub.Inventory(who(r).account.SpaceID))
		})
		forward := func(w http.ResponseWriter, r *http.Request) {
			a.edgeHub.Serve(who(r).account.SpaceID, who(r).owner, w, r)
		}
		for _, method := range []string{"POST", "PUT"} {
			mux.HandleFunc(method+" /api/edge/onvif", func(w http.ResponseWriter, r *http.Request) {
				a.edgeHub.AutoRecorderSetup(who(r).account.SpaceID, w, r)
			})
		}
		for _, method := range []string{"POST", "PUT", "DELETE"} {
			mux.HandleFunc(method+" /api/edge/{site}/onvif", func(w http.ResponseWriter, r *http.Request) { a.edgeHub.RecorderSetup(who(r).account.SpaceID, w, r) })
		}
		mux.HandleFunc("/api/edge/{site}/{channel}/{operation}", forward)
		mux.HandleFunc("/api/edge/{site}/{channel}/{operation}/{tail...}", forward)
	}
	mux.HandleFunc("GET /api/wall", func(w http.ResponseWriter, r *http.Request) {
		wall, err := a.gateway.Store.Wall(who(r).account)
		if err != nil {
			problem(w, 500, "无法读取监控墙")
			return
		}
		jsonReply(w, 200, wall)
	})
	mux.HandleFunc("PUT /api/wall", func(w http.ResponseWriter, r *http.Request) {
		actor := who(r).account
		var wall gb.Wall
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&wall) != nil || (wall.Size != 4 && wall.Size != 9 && wall.Size != 16) || len(wall.Slots) > 16 {
			problem(w, 400, "分屏配置无效")
			return
		}
		allowed := map[string]bool{}
		if a.edgeHub != nil {
			for _, site := range a.edgeHub.Inventory(actor.SpaceID) {
				for _, c := range site.Channels {
					if !c.RecordingOnly {
						allowed["edge/"+site.ID+"/"+c.ID] = true
					}
				}
			}
		}
		previous, err := a.gateway.Store.Wall(actor)
		if err != nil {
			problem(w, 500, "无法读取监控墙")
			return
		}
		for _, key := range previous.Slots {
			if strings.HasPrefix(key, "edge/") {
				allowed[key] = true
			}
		}
		for _, key := range wall.Slots {
			if key == "" || allowed[key] {
				continue
			}
			parts := strings.Split(key, "/")
			if len(parts) != 3 || parts[0] != "gb" || !a.gateway.Store.HasChannel(actor.SpaceID, parts[1], parts[2]) {
				problem(w, 404, "通道不属于当前空间")
				return
			}
		}
		if err := a.gateway.Store.SaveWall(actor, wall); err != nil {
			problem(w, 500, "无法保存监控墙")
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("PUT /api/devices/{id}/{channel}/ptz", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Enabled bool }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil {
			problem(w, 400, "云台设置无效")
			return
		}
		if err := a.gateway.Store.SetPTZ(who(r).account.SpaceID, r.PathValue("id"), r.PathValue("channel"), body.Enabled); err != nil {
			problem(w, 404, "未找到通道")
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/devices/{id}/{channel}/ptz", func(w http.ResponseWriter, r *http.Request) {
		id, ch := r.PathValue("id"), r.PathValue("channel")
		if !a.gateway.Store.CanPTZ(who(r).account.SpaceID, id, ch) {
			problem(w, 404, "通道未启用云台")
			return
		}
		var body struct {
			Action string
			Preset int
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil {
			problem(w, 400, "云台命令无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		if err := a.gateway.Control(ctx, id, ch, body.Action, body.Preset); err != nil {
			problem(w, 502, "云台命令未完成")
			return
		}
		w.WriteHeader(204)
	})
}

func (a *application) updateDevice(w http.ResponseWriter, r *http.Request) {
	var data struct{ Name, Kind, Secret string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&data) != nil {
		problem(w, 400, "设备配置格式无效")
		return
	}
	err := a.gateway.Store.UpdateDevice(who(r).account.SpaceID, r.PathValue("id"), data.Name, data.Kind, data.Secret)
	switch {
	case errors.Is(err, gb.ErrDeviceFields):
		problem(w, 400, "请检查设备名称、类型和注册密码")
	case errors.Is(err, sql.ErrNoRows):
		problem(w, 404, "未找到设备")
	case err != nil:
		problem(w, 500, "保存设备配置失败")
	default:
		w.WriteHeader(204)
	}
}
