package edge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type recorderTrack struct {
	Token       string `json:"token" xml:"TrackToken"`
	Kind        string `json:"-" xml:"TrackType"`
	Description string `json:"description" xml:"Description"`
}

type recorderRecording struct {
	Token  string `json:"token" xml:"RecordingToken"`
	Source struct {
		Name             string `json:"name" xml:"Name"`
		VideoSourceToken string `json:"-" xml:"VideoSourceToken"`
		ID               string `json:"id" xml:"SourceId"`
	} `json:"source" xml:"Source"`
	LiveProfiles []recorderProfile `json:"liveProfiles,omitempty" xml:"-"`
	AutoLive     bool              `json:"autoLive,omitempty" xml:"-"`
	Tracks       []recorderTrack   `json:"tracks" xml:"Track"`
}

type recorderInput struct {
	IP           string
	Port         int
	HTTPS        bool
	Username     string
	Password     string
	Recording    string
	Track        string
	LiveProfile  string
	Channel      string
	Name         string
	RecorderName string
	Kind         string
	Channels     []recorderSelection
}

type recorderSelection struct{ Recording, Track, LiveProfile, Name string }

type recorderBinding struct {
	Name         string
	ONVIF        onvifConfig
	LiveURI      string
	RecorderName string
	Kind         string
}

type recorderSettings struct {
	mu       sync.RWMutex
	file     string
	nets     []netip.Prefix
	bindings map[string]recorderBinding
}

func openRecorderSettings(file, networks string) (*recorderSettings, error) {
	s := &recorderSettings{file: file, bindings: map[string]recorderBinding{}}
	for _, field := range strings.Split(networks, ",") {
		if strings.TrimSpace(field) == "" {
			continue
		}
		p, err := netip.ParsePrefix(strings.TrimSpace(field))
		if err != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() {
			return nil, errors.New("invalid ONVIF LAN CIDR")
		}
		s.nets = append(s.nets, p)
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil && (json.Unmarshal(data, &s.bindings) != nil || s.bindings == nil) {
			return nil, errors.New("invalid ONVIF recorder state")
		}
	}
	return s, nil
}

func (s *recorderSettings) enabled() bool { return s != nil && s.file != "" && len(s.nets) > 0 }

func (s *recorderSettings) camera(input recorderInput) (onvifConfig, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(input.IP))
	if err != nil || !ip.Is4() || !ip.IsPrivate() || input.Port < 1 || input.Port > 65535 || len(input.Username) > 128 || input.Username == "" || len(input.Password) > 256 || input.Password == "" {
		return onvifConfig{}, errors.New("请填写录像机局域网 IPv4、端口和 ONVIF 账号")
	}
	for _, network := range s.nets {
		if !network.Contains(ip) {
			continue
		}
		scheme := "http"
		if input.HTTPS {
			scheme = "https"
		}
		device := url.URL{Scheme: scheme, Host: ip.String() + ":" + strconv.Itoa(input.Port), Path: "/onvif/device_service"}
		return onvifConfig{Device: device.String(), Username: input.Username, Password: input.Password, Recording: input.Recording, Track: input.Track, StrictHost: true}, nil
	}
	return onvifConfig{}, errors.New("录像机地址不在该网关允许接入的网段内")
}

func (s *recorderSettings) write(id string, binding *recorderBinding) error {
	if binding == nil {
		return s.commit(nil, id)
	}
	return s.commit(map[string]recorderBinding{id: *binding}, "")
}

func (s *recorderSettings) commit(add map[string]recorderBinding, remove string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if remove != "" {
		if _, ok := s.bindings[remove]; !ok {
			return os.ErrNotExist
		}
	}
	next := make(map[string]recorderBinding, len(s.bindings)+len(add))
	for key, row := range s.bindings {
		if key != remove {
			next[key] = row
		}
	}
	for key, row := range add {
		next[key] = row
	}
	if len(next) > 128 {
		return errors.New("recorder channel limit")
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err = atomicFile(s.file, raw); err != nil {
		return err
	}
	s.bindings = next
	return nil
}

func (e *edge) getChannel(id string) (channel, bool) {
	base, exists := e.channels[id]
	if e.recorders == nil {
		return base, exists
	}
	e.recorders.mu.RLock()
	binding, ok := e.recorders.bindings[id]
	e.recorders.mu.RUnlock()
	if !ok {
		return base, exists
	}
	if !exists {
		base = channel{ID: id, Name: binding.Name, Timezone: "Asia/Shanghai"}
	}
	config := binding.ONVIF
	if binding.Kind == "camera" {
		base.ONVIF = &config
	} else {
		base.RecordingONVIF = &config
		if base.ONVIF == nil && config.PTZ {
			base.ONVIF = &config
		}
	}
	base.DeviceKind = binding.Kind
	if base.DeviceKind == "" {
		base.DeviceKind = "recorder"
	}
	base.ManagedRecorder = true
	device, _ := url.Parse(config.Device)
	if device != nil {
		digest := sha256.Sum256([]byte(device.Host))
		base.RecorderID = fmt.Sprintf("%s-%x", base.DeviceKind, digest[:12])
	}
	base.RecorderName = binding.RecorderName
	if base.RecorderName == "" {
		base.RecorderName = "ONVIF 录像机"
	}
	if binding.LiveURI != "" {
		base.Stream = id
		base.ManagedStream = binding.LiveURI
	}
	return base, true
}

func (e *edge) channelList() []Channel {
	ids := make(map[string]bool, len(e.channels))
	for id := range e.channels {
		ids[id] = true
	}
	if e.recorders != nil {
		e.recorders.mu.RLock()
		for id := range e.recorders.bindings {
			ids[id] = true
		}
		e.recorders.mu.RUnlock()
	}
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	rows := make([]Channel, 0, len(keys))
	for _, id := range keys {
		c, ok := e.getChannel(id)
		if !ok {
			continue
		}
		rows = append(rows, Channel{ID: id, Name: c.Name, Timezone: c.Timezone, PTZ: c.ONVIF != nil && c.ONVIF.PTZ, History: c.Frigate != "" || c.hasProfileG() || c.Hikvision != nil, RecordingOnly: c.Stream == "", ManagedRecorder: c.ManagedRecorder, RecorderID: c.RecorderID, RecorderName: c.RecorderName, DeviceKind: c.DeviceKind})
	}
	return rows
}

func (e *edge) setupRecorder(w http.ResponseWriter, r *http.Request) {
	if !e.recorders.enabled() {
		failure(w, 403, "此现场网关尚未开放网页配置 ONVIF")
		return
	}
	var input recorderInput
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, recorderRequestLimit)).Decode(&input) != nil {
		failure(w, 400, "录像机配置无效")
		return
	}
	if r.Method == http.MethodDelete {
		if !identifier.MatchString(input.Channel) {
			failure(w, 400, "通道无效")
			return
		}
		if err := e.recorders.write(input.Channel, nil); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				failure(w, 404, "此通道没有网页配置的录像来源")
			} else {
				failure(w, 500, "无法移除录像来源")
			}
			return
		}
		w.WriteHeader(204)
		return
	}
	c, err := e.recorders.camera(input)
	if err != nil {
		failure(w, 400, err.Error())
		return
	}
	if input.Kind == "camera" {
		e.setupCamera(w, r, c, input)
		return
	}
	search, err := c.service(r.Context(), searchNS)
	if err == nil {
		_, err = c.service(r.Context(), replayNS)
	}
	if err != nil {
		if errors.Is(err, errUnsupported) {
			failure(w, 422, "录像机未提供 ONVIF Profile G 的录像查询和回放服务")
		} else {
			failure(w, 502, "无法连接录像机，请检查地址、ONVIF 账号及网关网络")
		}
		return
	}
	if r.Method == http.MethodPost {
		rows, err := c.discoverRecordings(r.Context(), search)
		if err != nil {
			failure(w, 502, "录像机未能返回录像通道")
			return
		}
		_, profiles, _ := c.mediaProfiles(r.Context())
		for i := range rows {
			matched := rows[i].liveProfiles(profiles)
			rows[i].AutoLive = len(matched) > 0
			if len(matched) == 0 && rows[i].Source.VideoSourceToken == "" {
				matched = profiles
			}
			rows[i].LiveProfiles = matched
		}
		jsonResponse(w, rows)
		return
	}
	selections := input.Channels
	batch := len(selections) > 0
	if !batch {
		selections = []recorderSelection{{input.Recording, input.Track, input.LiveProfile, input.Name}}
	}
	name := strings.TrimSpace(input.RecorderName)
	if len(name) > 180 || (batch && (name == "" || input.Channel != "" || len(selections) > 32)) {
		failure(w, 400, "请填写录像机名称，每次最多接入 32 路通道")
		return
	}
	var media string
	var profiles []recorderProfile
	for _, selection := range selections {
		if selection.LiveProfile != "" {
			media, profiles, err = c.mediaProfiles(r.Context())
			if err != nil {
				failure(w, 422, "无法读取录像机的实时通道")
				return
			}
			break
		}
	}
	bindings := make(map[string]recorderBinding, len(selections))
	var id string
	for _, selection := range selections {
		var binding recorderBinding
		var status int
		id, binding, status, err = e.prepareRecorderChannel(r.Context(), c, selection, search, media, profiles, input.Channel)
		if err != nil {
			failure(w, status, err.Error())
			return
		}
		if _, duplicate := bindings[id]; duplicate {
			failure(w, 400, "重复选择了同一录像通道")
			return
		}
		binding.RecorderName = name
		bindings[id] = binding
	}
	if !batch && bindings[id].LiveURI != "" {
		if err = e.configureLive(r.Context(), id, bindings[id].LiveURI, false); err != nil {
			failure(w, 502, "现场实时视频服务不可用")
			return
		}
	}
	if err = e.recorders.commit(bindings, ""); err != nil {
		failure(w, 500, "无法保存录像机通道")
		return
	}
	if !batch {
		jsonResponse(w, map[string]string{"channel": id})
		return
	}
	jsonResponse(w, map[string]any{"added": len(bindings)})
}

func (e *edge) prepareRecorderChannel(ctx context.Context, c onvifConfig, sel recorderSelection, search, media string, profiles []recorderProfile, id string) (string, recorderBinding, int, error) {
	c.Recording, c.Track = sel.Recording, sel.Track
	var binding recorderBinding
	if c.Recording == "" || len(c.Recording) > 128 || c.Track == "" || len(c.Track) > 128 || len(sel.LiveProfile) > 128 {
		return "", binding, 400, errors.New("请选择有效的录像通道及视频轨道")
	}
	record, err := c.recordingInfo(ctx, search)
	if err != nil {
		return "", binding, 502, errors.New("无法确认录像通道")
	}
	found := false
	for _, track := range videoTracks(record.Tracks) {
		if track.Token == c.Track {
			found = true
			break
		}
	}
	if !found {
		return "", binding, 422, errors.New("录像机未提供所选的视频轨道，请重新检测")
	}
	if _, err = c.replayURI(ctx); err != nil {
		return "", binding, 422, errors.New("录像机未提供可用的 ONVIF 回放地址")
	}
	name := strings.TrimSpace(sel.Name)
	if id == "" {
		if name == "" || len(name) > 180 {
			return "", binding, 400, errors.New("请填写有效的通道名称")
		}
		digest := sha256.Sum256([]byte(c.Device + "\x00" + c.Recording + "\x00" + c.Track))
		id = fmt.Sprintf("record-%x", digest[:12])
	} else if base, ok := e.channels[id]; !ok || base.Stream == "" || sel.LiveProfile != "" {
		return "", binding, 404, errors.New("无法使用所选的现有实时通道")
	}
	binding = recorderBinding{Name: name, ONVIF: c}
	if sel.LiveProfile == "" {
		return id, binding, 0, nil
	}
	matched := record.liveProfiles(profiles)
	if len(matched) == 0 && record.Source.VideoSourceToken == "" {
		matched = profiles
	}
	for _, profile := range matched {
		if profile.Token != sel.LiveProfile {
			continue
		}
		binding.LiveURI, err = c.streamURI(ctx, media, sel.LiveProfile)
		if err != nil {
			return "", binding, 422, errors.New("录像机未返回可用的实时地址")
		}
		if profile.PTZ != nil && profile.PTZ.Token != "" {
			_, err = c.service(ctx, ptzNS)
			binding.ONVIF.PTZ = err == nil
			binding.ONVIF.Profile = profile.Token
		}
		return id, binding, 0, nil
	}
	return "", binding, 422, errors.New("实时通道与录像来源无法关联，请重新检测")
}

func (c *onvifConfig) discoverRecordings(ctx context.Context, target string) ([]recorderRecording, error) {
	var search struct{ SearchToken string }
	if err := c.soap(ctx, target, searchNS, "FindRecordings", `<x:Scope/><x:MaxMatches>128</x:MaxMatches><x:KeepAliveTime>PT10S</x:KeepAliveTime>`, &search); err != nil {
		return nil, err
	}
	if search.SearchToken == "" {
		return nil, unavailable
	}
	token := "<x:SearchToken>" + esc(search.SearchToken) + "</x:SearchToken>"
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.soap(ctx, target, searchNS, "EndSearch", token, nil)
	}()
	rows := []recorderRecording{}
	for ctx.Err() == nil {
		var result struct {
			List struct {
				State   string              `xml:"SearchState"`
				Records []recorderRecording `xml:"RecordingInformation"`
			} `xml:"ResultList"`
		}
		if err := c.soap(ctx, target, searchNS, "GetRecordingSearchResults", token+`<x:MinResults>1</x:MinResults><x:MaxResults>64</x:MaxResults><x:WaitTime>PT1S</x:WaitTime>`, &result); err != nil {
			return nil, err
		}
		for _, record := range result.List.Records {
			record.Tracks = videoTracks(record.Tracks)
			if record.Token != "" && len(record.Tracks) > 0 {
				rows = append(rows, record)
			}
		}
		if result.List.State == "Completed" || len(rows) >= 128 {
			return rows, nil
		}
		if len(result.List.Records) == 0 {
			select {
			case <-ctx.Done():
			case <-time.After(150 * time.Millisecond):
			}
		}
	}
	return nil, ctx.Err()
}

func (c *onvifConfig) recordingInfo(ctx context.Context, target string) (recorderRecording, error) {
	var result struct {
		Recording recorderRecording `xml:"RecordingInformation"`
	}
	err := c.soap(ctx, target, searchNS, "GetRecordingInformation", "<x:RecordingToken>"+esc(c.Recording)+"</x:RecordingToken>", &result)
	return result.Recording, err
}

func videoTracks(tracks []recorderTrack) []recorderTrack {
	out := make([]recorderTrack, 0, len(tracks))
	for _, track := range tracks {
		if track.Kind == "Video" && track.Token != "" {
			out = append(out, track)
		}
	}
	return out
}
