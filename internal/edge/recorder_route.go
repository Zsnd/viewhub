package edge

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
)

const recorderRequestLimit = 32768

func (h *Hub) recorderRoute(space int64, ip netip.Addr) (*connection, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var match *connection
	best := -1
	tied := false
	for _, site := range h.managed {
		if site.Space != space || site.Provider != "edge" {
			continue
		}
		prefix, err := netip.ParsePrefix(site.LAN)
		if err != nil || !prefix.Contains(ip) || prefix.Bits() < best {
			continue
		}
		if prefix.Bits() == best {
			tied = true
			continue
		}
		best = prefix.Bits()
		match = h.sites[site.ID]
		tied = false
	}
	return match, tied
}

func (h *Hub) AutoRecorderSetup(space int64, w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, recorderRequestLimit))
	var request struct{ IP string }
	if err != nil || json.Unmarshal(data, &request) != nil {
		failure(w, 400, "录像机配置无效")
		return
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(request.IP))
	if err != nil || !ip.Is4() || !ip.IsPrivate() {
		failure(w, 400, "请填写录像机的局域网 IPv4 地址")
		return
	}
	site, tied := h.recorderRoute(space, ip)
	if tied {
		failure(w, 409, "这个地址属于多个现场网络，请联系管理员确认接入位置")
		return
	}
	if site == nil {
		failure(w, 422, "当前空间没有可以访问这个地址的现场连接")
		return
	}
	if !site.online() {
		failure(w, 503, "录像机所在现场暂未连接")
		return
	}
	r = r.Clone(r.Context())
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.URL.Path, r.URL.RawPath, r.URL.RawQuery = "/api/site/onvif", "", ""
	site.broker.call(w, r)
}
