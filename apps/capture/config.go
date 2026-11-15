package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

type Config struct {
	FFmpeg      string `json:"ffmpeg"`
	Camera      string `json:"camera"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	MaxBitrate  int    `json:"maxBitrate"`
	Quality     string `json:"quality"`
	BitrateMode string `json:"bitrateMode"`
	Codec       string `json:"codec"`
	inputFPS    int
	Server      string `json:"server"`
	Port        int    `json:"port"`
	Realm       string `json:"realm"`
	ServerID    string `json:"serverId"`
	DeviceID    string `json:"deviceId"`
	ChannelID   string `json:"channelId"`
	Password    string `json:"password,omitempty"`
	LocalIP     string `json:"localIp"`
	SIPPort     int    `json:"sipPort"`
}

func DefaultConfig() Config {
	camera := "Web Camera"
	if runtime.GOOS == "linux" {
		camera = "/dev/video0"
	}
	c := Config{FFmpeg: "ffmpeg", Camera: camera, Width: 1280, Height: 720, FPS: 30, Quality: "high", BitrateMode: "auto", Codec: "h264", Server: "127.0.0.1", Port: 15060, Realm: "3402000000", ServerID: "34020000002000000002", DeviceID: "34020000001320000901", ChannelID: "34020000001310000901", SIPPort: 15062}
	c.MaxBitrate = recommendedMaxBitrate(c)
	return c
}

var code = regexp.MustCompile(`^[0-9]{20}$`)
var domain = regexp.MustCompile(`^[0-9]{10}$`)

func (c Config) Validate() error {
	if err := c.validateVideo(); err != nil {
		return err
	}
	if c.FFmpeg == "" || c.Camera == "" {
		return errors.New("请选择摄像头并填写 FFmpeg 路径")
	}
	if c.Port < 1 || c.Port > 65535 || c.SIPPort < 1024 || c.SIPPort > 65535 {
		return errors.New("平台端口须为 1–65535，本地 SIP 端口须为 1024–65535")
	}
	if !code.MatchString(c.ServerID) || !code.MatchString(c.DeviceID) || !code.MatchString(c.ChannelID) || !domain.MatchString(c.Realm) {
		return errors.New("国标编号须为 20 位数字，域须为 10 位数字")
	}
	if c.Password == "" || len(c.Password) > 256 {
		return errors.New("请填写注册密码（最多 256 字节）")
	}
	if len(c.Server) == 0 || len(c.Server) > 253 {
		return errors.New("请填写平台 IP 或主机名")
	}
	if c.LocalIP != "" {
		ip := net.ParseIP(c.LocalIP)
		if ip == nil || ip.To4() == nil || ip.IsUnspecified() {
			return errors.New("本机地址须为可用的 IPv4 地址")
		}
	}
	return nil
}

func (c Config) validateVideo() error {
	if c.Codec != "" && c.Codec != "h264" && c.Codec != "h265" {
		return errors.New("当前支持 H.264 / H.265；AV1 尚未接入")
	}
	if c.Width < 160 || c.Width > 3840 || c.Height < 120 || c.Height > 2160 || c.Width%2 != 0 || c.Height%2 != 0 {
		return errors.New("视频尺寸必须为偶数，宽 160–3840、高 120–2160")
	}
	if c.FPS < 1 || c.FPS > 60 || c.MaxBitrate < 128 || c.MaxBitrate > 20000 {
		return errors.New("帧率须为 1–60，最大码率须为 128–20000 kbps")
	}
	if c.Quality != "standard" && c.Quality != "high" && c.Quality != "ultra" {
		return errors.New("请选择标准、高清或更高画质")
	}
	if c.BitrateMode != "auto" && c.BitrateMode != "manual" {
		return errors.New("最大码率模式无效")
	}
	return nil
}
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	if err != nil {
		return c, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(b, &fields); err != nil {
		return c, err
	}
	if _, present := fields["maxBitrate"]; !present {
		if legacy, ok := fields["bitrate"]; ok {
			if err = json.Unmarshal(legacy, &c.MaxBitrate); err != nil {
				return c, err
			}
			c.BitrateMode = "manual" // Preserve the user's former numeric limit.
		}
	}
	if c.BitrateMode == "auto" {
		c.MaxBitrate = recommendedMaxBitrate(c)
	}
	return c, err
}
func SaveConfig(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("保存配置: %w", err)
	}
	return nil
}
