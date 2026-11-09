package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecorderSetupDiscoversBindsAndRestoresRecordingSource(t *testing.T) {
	const device = "10.88.1.24"
	const source = "<Source><Name>Entrance</Name><VideoSourceToken>camera-1</VideoSourceToken></Source>"
	const track = "<Track><TrackToken>video</TrackToken><TrackType>Video</TrackType></Track>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/streams" {
			if r.Method != http.MethodPatch || r.URL.Query().Get("src") != "rtsp://camera-account:test-secret@10.88.1.24:554/live" {
				t.Error("unexpected go2rtc stream request")
			}
			w.WriteHeader(204)
			return
		}
		header := r.Header.Get("Content-Type")
		action := strings.TrimSuffix(header[strings.LastIndex(header, "/")+1:], "\"")
		var reply string
		switch action {
		case "GetServices":
			reply = fmt.Sprintf("<Service><Namespace>%s</Namespace><XAddr>http://%s/search</XAddr></Service><Service><Namespace>%s</Namespace><XAddr>http://%s/replay</XAddr></Service><Service><Namespace>%s</Namespace><XAddr>http://%s/media</XAddr></Service>", searchNS, device, replayNS, device, mediaNS, device)
		case "FindRecordings", "FindEvents":
			reply = "<SearchToken>search</SearchToken>"
		case "GetRecordingSearchResults":
			reply = "<ResultList><SearchState>Completed</SearchState><RecordingInformation><RecordingToken>rec1</RecordingToken>" + source + track + "<Track><TrackToken>audio</TrackToken><TrackType>Audio</TrackType></Track></RecordingInformation></ResultList>"
		case "GetRecordingInformation":
			reply = "<RecordingInformation>" + source + track + "</RecordingInformation>"
		case "GetProfiles":
			reply = `<Profiles token="wrong"><Name>Other</Name><VideoSourceConfiguration><SourceToken>camera-2</SourceToken></VideoSourceConfiguration></Profiles><Profiles token="live"><Name>Main</Name><VideoSourceConfiguration><SourceToken>camera-1</SourceToken></VideoSourceConfiguration></Profiles>`
		case "GetStreamUri":
			reply = "<MediaUri><Uri>rtsp://" + device + ":554/live</Uri></MediaUri>"
		case "GetReplayUri":
			reply = "<Uri>rtsp://" + device + ":554/history</Uri>"
		case "GetEventSearchResults":
			event := func(at string, active bool) string {
				return fmt.Sprintf("<Result><RecordingToken>rec1</RecordingToken><TrackToken>video</TrackToken><Time>%s</Time><Event><Topic>RecordingHistory/Track/State</Topic><Message><Message><Data><SimpleItem Name=\"IsDataPresent\" Value=\"%t\"/></Data></Message></Message></Event></Result>", at, active)
			}
			reply = "<ResultList><SearchState>Completed</SearchState>" + event("2026-11-08T00:00:00Z", true) + event("2026-11-08T00:00:10Z", false) + event("2026-11-08T00:00:25Z", true) + event("2026-11-08T00:00:40Z", false) + "</ResultList>"
		case "EndSearch":
		default:
			t.Errorf("unexpected ONVIF operation %s", action)
			w.WriteHeader(500)
			return
		}
		fmt.Fprintf(w, "<Envelope><Body><%sResponse>%s</%sResponse></Body></Envelope>", action, reply, action)
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	prior := http.DefaultTransport
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil || (host != device && host != "go2rtc.test") {
			return nil, fmt.Errorf("unexpected address %q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target.Host)
	}}
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = prior; transport.CloseIdleConnections() }()

	file := filepath.Join(t.TempDir(), "recorders.json")
	store, err := openRecorderSettings(file, "10.88.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	base := channel{ID: "entrance", Name: "Entrance", Stream: "entrance", Frigate: "entrance", Timezone: "Asia/Shanghai", ONVIF: &onvifConfig{PTZ: true}}
	e := &edge{recorders: store, rtc: address("http://go2rtc.test"), channels: map[string]channel{base.ID: base}}
	call := func(method string, in recorderInput) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(in)
		w := httptest.NewRecorder()
		e.setupRecorder(w, httptest.NewRequest(method, "/api/site/onvif", bytes.NewReader(data)))
		return w
	}
	input := recorderInput{IP: device, Port: 80, Username: "camera-account", Password: "test-secret"}
	w := call("POST", input)
	var rows []recorderRecording
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 || len(rows[0].Tracks) != 1 || rows[0].Tracks[0].Token != "video" || strings.Contains(w.Body.String(), input.Password) {
		t.Fatalf("unexpected detection response %d %s", w.Code, w.Body)
	}
	if !rows[0].AutoLive || len(rows[0].LiveProfiles) != 1 || rows[0].LiveProfiles[0].Token != "live" {
		t.Fatalf("wrong live match: %+v", rows)
	}
	outside := input
	outside.IP = "10.88.2.24"
	if w := call("POST", outside); w.Code != 400 {
		t.Fatalf("outside LAN status %d", w.Code)
	}
	input.Recording, input.Track, input.Channel = "rec1", "video", "entrance"
	if w := call("PUT", input); w.Code != 200 {
		t.Fatalf("bind %d %s", w.Code, w.Body)
	}
	reopened, err := openRecorderSettings(file, "10.88.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	e.recorders = reopened
	bound, _ := e.getChannel("entrance")
	if !bound.ManagedRecorder || !bound.hasProfileG() || bound.usesFrigateVOD() || bound.Stream != base.Stream || bound.ONVIF != base.ONVIF {
		t.Fatal("recording binding did not preserve live and PTZ settings")
	}
	history := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/site/entrance/recordings?date=2026-11-08", nil)
	req.SetPathValue("id", "entrance")
	e.recordings(history, req)
	var day struct {
		Source   string
		Segments []interval
	}
	if history.Code != 200 || json.Unmarshal(history.Body.Bytes(), &day) != nil || day.Source != "onvif" || len(day.Segments) != 2 || day.Segments[1].Start-day.Segments[0].End != 15 {
		t.Fatalf("timeline %d %s", history.Code, history.Body)
	}
	input.Channel, input.Name = "", "Back door"
	if w := call("PUT", input); w.Code != 200 {
		t.Fatalf("new recording channel %d %s", w.Code, w.Body)
	}
	catalog := e.channelList()
	if len(catalog) != 2 || !catalog[1].RecordingOnly || !catalog[1].History {
		t.Fatalf("unexpected channels %+v", catalog)
	}
	input.LiveProfile = "wrong"
	if w := call("PUT", input); w.Code != 422 {
		t.Fatalf("wrong profile status %d", w.Code)
	}
	input.LiveProfile = "live"
	if w := call("PUT", input); w.Code != 200 {
		t.Fatalf("configure live %d %s", w.Code, w.Body)
	}
	catalog = e.channelList()
	if len(catalog) != 2 || catalog[1].RecordingOnly || !catalog[1].History {
		t.Fatalf("combined channel missing %+v", catalog)
	}
	if w := call("DELETE", recorderInput{Channel: "entrance"}); w.Code != 204 {
		t.Fatalf("unbind %d %s", w.Code, w.Body)
	}
	restored, _ := e.getChannel("entrance")
	if restored.ManagedRecorder || !restored.usesFrigateVOD() {
		t.Fatal("Frigate source was not restored")
	}
}

func TestRecorderSetupRejectsOtherSpace(t *testing.T) {
	h := &Hub{sites: map[string]*connection{"main": {Site: Site{Space: 1}}}}
	r := httptest.NewRequest("POST", "/api/edge/main/onvif", nil)
	r.SetPathValue("site", "main")
	w := httptest.NewRecorder()
	h.RecorderSetup(2, w, r)
	if w.Code != 404 {
		t.Fatalf("status %d", w.Code)
	}
}
