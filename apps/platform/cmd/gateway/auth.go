package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"viewhub/apps/platform/gb"
)

const authCookie = "viewhub_session"

type identityKey struct{}
type identity struct {
	account gb.Account
	owner   string
}

func who(r *http.Request) identity {
	id, _ := r.Context().Value(identityKey{}).(identity)
	return id
}

func (a *application) protect(routes http.Handler) http.Handler {
	limiter := rate.NewLimiter(4, 12)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; media-src 'self' blob:; worker-src 'self' blob:; frame-ancestors 'none'")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			routes.ServeHTTP(w, r)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-Playback-Request") != "1" {
			problem(w, 403, "请求未授权")
			return
		}
		if r.URL.Path == "/api/auth/login" && r.Method == "POST" {
			if !limiter.Allow() {
				problem(w, 429, "登录请求过多，请稍后重试")
				return
			}
			routes.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(authCookie)
		if err != nil || len(cookie.Value) != 64 {
			problem(w, 401, "请先登录")
			return
		}
		key := gb.SessionKey(cookie.Value)
		account, err := a.gateway.Store.Session(key)
		if err != nil {
			problem(w, 401, "登录已过期，请重新登录")
			return
		}
		switch r.URL.Path {
		case "/api/auth/me", "/api/auth/logout", "/api/auth/password":
		default:
			if account.MustChange {
				problem(w, 403, "请先修改初始密码")
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/") && !account.Admin {
			problem(w, 403, "仅系统管理员可以操作")
			return
		}
		ctx := context.WithValue(r.Context(), identityKey{}, identity{account, key})
		routes.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (a *application) cookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: authCookie, Value: value, Path: "/", HttpOnly: true, Secure: a.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (a *application) login(w http.ResponseWriter, r *http.Request) {
	var form struct{ Username, Password string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&form) != nil {
		problem(w, 400, "登录参数无效")
		return
	}
	token, err := a.gateway.Store.Login(form.Username, form.Password)
	if errors.Is(err, gb.ErrCredentials) {
		problem(w, 401, "账号或密码错误")
		return
	}
	if err != nil {
		problem(w, 500, "暂时无法登录")
		return
	}
	if old, err := r.Cookie(authCookie); err == nil {
		key := gb.SessionKey(old.Value)
		_ = a.gateway.Store.Logout(key)
		a.sessions.revoke(key, 0)
	}
	a.cookie(w, token, int(gb.LoginDuration/time.Second))
	account, err := a.gateway.Store.Session(gb.SessionKey(token))
	if err != nil {
		problem(w, 500, "暂时无法读取账号")
		return
	}
	jsonReply(w, 200, account)
}
func (a *application) logout(w http.ResponseWriter, r *http.Request) {
	id := who(r)
	if err := a.gateway.Store.Logout(id.owner); err != nil {
		problem(w, 500, "暂时无法退出")
		return
	}
	a.sessions.revoke(id.owner, 0)
	a.stopTalks(id.owner, 0, "")
	a.cookie(w, "", -1)
	w.WriteHeader(204)
}
func (a *application) password(w http.ResponseWriter, r *http.Request) {
	var form struct{ Current, Password string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&form) != nil {
		problem(w, 400, "密码参数无效")
		return
	}
	id := who(r)
	err := a.gateway.Store.ChangePassword(id.account, form.Current, form.Password)
	if errors.Is(err, gb.ErrCredentials) {
		problem(w, 400, "当前密码错误")
		return
	}
	if errors.Is(err, gb.ErrAccountFields) {
		problem(w, 400, "新密码需为 12–72 字节，且与当前密码不同")
		return
	}
	if err != nil {
		problem(w, 500, "暂时无法修改密码")
		return
	}
	a.sessions.revoke("", id.account.ID)
	a.stopTalks("", id.account.ID, "")
	a.cookie(w, "", -1)
	w.WriteHeader(204)
}
func (a *application) accounts(w http.ResponseWriter, r *http.Request) {
	list, err := a.gateway.Store.Accounts()
	if err != nil {
		problem(w, 500, "读取账号失败")
		return
	}
	jsonReply(w, 200, list)
}
func (a *application) createAccount(w http.ResponseWriter, r *http.Request) {
	var form struct{ Username, Space, Password string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&form) != nil {
		problem(w, 400, "账号参数无效")
		return
	}
	account, err := a.gateway.Store.CreateCustomer(form.Username, form.Space, form.Password)
	switch {
	case errors.Is(err, gb.ErrAccountFields):
		problem(w, 400, "账号需为 3–40 位字母、数字或 . _ -，密码至少 12 位")
	case errors.Is(err, gb.ErrAccountExists):
		problem(w, 409, "登录账号已存在")
	case err != nil:
		problem(w, 500, "创建账号失败")
	default:
		jsonReply(w, 201, account)
	}
}
