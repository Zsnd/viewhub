package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"viewhub/apps/platform/gb"
)

func (a *application) viewHubRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/devices/{id}", a.updateDevice)
	mux.HandleFunc("DELETE /api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		err := a.gateway.DeleteDevice(who(r).account.SpaceID, id)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 404, "未找到设备")
			return
		}
		if err != nil {
			problem(w, 500, "删除设备失败")
			return
		}
		a.sessions.removeDevice(id)
		a.stopTalks("", 0, id)
		w.WriteHeader(204)
	})
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
		for _, key := range wall.Slots {
			if key == "" {
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
		var body struct{ Enabled, Automatic bool }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil {
			problem(w, 400, "云台设置无效")
			return
		}
		var err error
		if body.Automatic {
			err = a.gateway.Store.ResetPTZ(who(r).account.SpaceID, r.PathValue("id"), r.PathValue("channel"))
		} else {
			err = a.gateway.Store.SetPTZ(who(r).account.SpaceID, r.PathValue("id"), r.PathValue("channel"), body.Enabled)
		}
		if err != nil {
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
			Hold   bool
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil {
			problem(w, 400, "云台命令无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		var err error
		if body.Hold {
			err = a.gateway.ControlHold(ctx, id, ch, body.Action)
		} else {
			err = a.gateway.Control(ctx, id, ch, body.Action, body.Preset)
		}
		if err != nil {
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
