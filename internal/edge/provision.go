package edge

import (
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type managedSite struct {
	Site
	PasswordHash string
	LAN          string
	Timezone     string
}

func atomicFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".viewhub-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (h *Hub) loadManaged(dir string) error {
	h.provisionDir = dir
	if dir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sites.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && json.Unmarshal(raw, &h.managed) != nil {
		return errors.New("invalid managed gateways")
	}
	for _, site := range h.managed {
		if !validSite(site.Site) || h.sites[site.ID] != nil || !strings.HasPrefix(site.PasswordHash, "$7$") {
			return errors.New("invalid managed gateway")
		}
		h.addSite(site.Site)
	}
	return h.writePasswords(h.managed)
}

func (h *Hub) writePasswords(sites []managedSite) error {
	var data strings.Builder
	for _, site := range sites {
		fmt.Fprintf(&data, "%s:%s\n", site.ID, site.PasswordHash)
	}
	return atomicFile(filepath.Join(h.provisionDir, "passwords"), []byte(data.String()))
}

func privateLAN(raw string) (string, error) {
	network, err := netip.ParsePrefix(strings.TrimSpace(raw))
	if err != nil || !network.Addr().Is4() || !network.Addr().IsPrivate() {
		return "", errors.New("请填写局域网网段，例如 192.168.1.0/24")
	}
	network = network.Masked()
	if !network.Addr().IsPrivate() || network.Addr().As4()[0] == 10 && network.Bits() < 8 ||
		network.Addr().As4()[0] == 172 && network.Bits() < 12 || network.Addr().As4()[0] == 192 && network.Bits() < 16 {
		return "", errors.New("网段必须在 IPv4 私有地址范围内")
	}
	return network.String(), nil
}

func mqttPasswordHash(password string) string {
	salt := randomBytes(12)
	key, err := pbkdf2.Key(sha512.New, password, salt, 101, 64)
	if err != nil {
		panic(err)
	}
	return "$7$101$" + base64.StdEncoding.EncodeToString(salt) + "$" + base64.StdEncoding.EncodeToString(key)
}

func (h *Hub) Provision(space int64, origin, version string, w http.ResponseWriter, r *http.Request) {
	if h.provisionDir == "" {
		failure(w, 503, "此服务器未启用现场网关登记")
		return
	}
	var input struct{ Name, LAN string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&input) != nil {
		failure(w, 400, "登记参数无效")
		return
	}
	name := strings.TrimSpace(input.Name)
	lan, err := privateLAN(input.LAN)
	if err != nil {
		failure(w, 400, err.Error())
		return
	}
	if name == "" || utf8.RuneCountInString(name) > 60 {
		failure(w, 400, "请填写 60 字以内的网关名称")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, site := range h.sites {
		if site.Space == space {
			count++
		}
	}
	if count >= 32 {
		failure(w, 409, "当前空间的网关数量已达上限")
		return
	}
	password := hex.EncodeToString(randomBytes(24))
	site := managedSite{Site: Site{ID: "edge-" + hex.EncodeToString(randomBytes(10)), Name: name, Space: space, Provider: "edge", Token: hex.EncodeToString(randomBytes(32))}, LAN: lan, PasswordHash: mqttPasswordHash(password)}
	next := append(append([]managedSite(nil), h.managed...), site)
	state, _ := json.Marshal(next)
	if err = h.writePasswords(next); err != nil {
		failure(w, 500, "无法登记 MQTT 账号")
		return
	}
	if err = atomicFile(filepath.Join(h.provisionDir, "sites.json"), state); err != nil {
		_ = h.writePasswords(h.managed)
		failure(w, 500, "无法保存网关")
		return
	}
	h.managed = next
	h.addSite(site.Site)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": site.ID, "compose": gatewayCompose(site, password, origin, version)})
}

func gatewayCompose(site managedSite, password, origin, version string) string {
	id := site.ID
	dockerfile := fmt.Sprintf("FROM debian:bookworm-slim\nRUN apt-get update && apt-get install -y --no-install-recommends ffmpeg ca-certificates wget && rm -rf /var/lib/apt/lists/*\nRUN wget -q -O /viewhub-edge %s/edge-release/%s/viewhub-edge-`dpkg --print-architecture` && chmod 755 /viewhub-edge\nENTRYPOINT [\"/viewhub-edge\"]\n", origin, version)
	wss := strings.NewReplacer("https://", "wss://", "http://", "ws://").Replace(origin)
	config := map[string]any{
		"name": "viewhub-" + id,
		"services": map[string]any{
			"edge": map[string]any{
				"build":      map[string]string{"dockerfile_inline": dockerfile},
				"restart":    "unless-stopped",
				"ports":      []string{"18558:18558/udp"},
				"depends_on": []string{"go2rtc"},
				"environment": map[string]string{
					"VIEWHUB_ID": id, "VIEWHUB_EDGE_TOKEN": site.Token,
					"VIEWHUB_MQTT_URL": wss + "/mqtt", "VIEWHUB_MQTT_USER": id, "VIEWHUB_MQTT_PASSWORD": password,
					"VIEWHUB_MEDIA_URL": wss + "/edge/media/" + id, "VIEWHUB_ONVIF_LAN": site.LAN,
					"VIEWHUB_ONVIF_STATE": "/var/lib/viewhub/recorders.json", "VIEWHUB_REPLAY_ICE_PORT": "18558",
				},
				"volumes": []string{"recorder-data:/var/lib/viewhub"},
				"configs": []map[string]string{{"source": "channels", "target": "/config/channels.json"}},
			},
			"go2rtc": map[string]any{
				"image": "alexxit/go2rtc:1.9.15", "restart": "unless-stopped",
				"ports":   []string{"18556:18556/tcp", "18556:18556/udp"},
				"configs": []map[string]string{{"source": "go2rtc", "target": "/config/go2rtc.yaml"}},
			},
		},
		"configs": map[string]any{
			"channels": map[string]string{"content": "{\"channels\":[]}"},
			"go2rtc":   map[string]string{"content": "api:\n  listen: ':1984'\nwebrtc:\n  listen: ':18556'\n  candidates:\n    - stun:18556\nstreams: {}\n"},
		},
		"volumes": map[string]any{"recorder-data": map[string]any{}},
	}
	body, _ := json.MarshalIndent(config, "", "  ")
	return string(body) + "\n"
}
