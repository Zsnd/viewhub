package edge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTimelineKeepsRecordingGaps(t *testing.T) {
	got := mergeIntervals([]interval{{Start: 130, End: 160}, {Start: 100, End: 120}, {Start: 110, End: 121}})
	want := []interval{{Start: 100, End: 121}, {Start: 130, End: 160}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestProfileGReadsTrackStateAndClosesSearch(t *testing.T) {
	var ended bool
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.Header.Get("Content-Type")
		var op, data string
		switch {
		case strings.Contains(action, "/GetServices"):
			op = "GetServices"
			data = fmt.Sprintf("<Service><Namespace>%s</Namespace><XAddr>%s/search</XAddr></Service><Service><Namespace>%s</Namespace><XAddr>%s/replay</XAddr></Service>", searchNS, server.URL, replayNS, server.URL)
		case strings.Contains(action, "/GetReplayUri"):
			op, data = "GetReplayUri", "<Uri>rtsp://127.0.0.1/replay</Uri>"
		case strings.Contains(action, "/GetRecordingInformation"):
			op, data = "GetRecordingInformation", "<RecordingInformation><Track><TrackToken>a</TrackToken><TrackType>Audio</TrackType></Track><Track><TrackToken>v</TrackToken><TrackType>Video</TrackType></Track></RecordingInformation>"
		case strings.Contains(action, "/FindEvents"):
			op, data = "FindEvents", "<SearchToken>test-search</SearchToken>"
		case strings.Contains(action, "/GetEventSearchResults"):
			op = "GetEventSearchResults"
			event := func(second int, active bool) string {
				return fmt.Sprintf(`<Result><RecordingToken>r</RecordingToken><TrackToken>v</TrackToken><Time>2026-11-08T00:00:%02dZ</Time><Event><Topic>tns1:RecordingHistory/Track/State</Topic><Message><Message><Data><SimpleItem Name="IsDataPresent" Value="%t"/></Data></Message></Message></Event></Result>`, second, active)
			}
			data = "<ResultList><SearchState>Completed</SearchState>" + event(0, true) + event(10, false) + event(25, true) + event(40, false) + "</ResultList>"
		case strings.Contains(action, "/EndSearch"):
			ended = true
			op = "EndSearch"
		default:
			t.Errorf("unexpected SOAP request")
			w.WriteHeader(500)
			return
		}
		fmt.Fprintf(w, "<Envelope><Body><%sResponse>%s</%sResponse></Body></Envelope>", op, data, op)
	}))
	defer server.Close()
	cfg := onvifConfig{Device: server.URL + "/device", Recording: "r"}
	start := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	e := &edge{}
	rows, source, err := e.getRecordings(context.Background(), channel{ONVIF: &cfg, Frigate: "preserve-as-optional"}, start, start.Add(time.Minute))
	if source != "onvif" {
		t.Fatalf("got source %q", source)
	}
	if err != nil {
		t.Fatal(err)
	}
	want := []interval{{Start: start.Unix(), End: start.Unix() + 10, ProfileG: true}, {Start: start.Unix() + 25, End: start.Unix() + 40, ProfileG: true}}
	if !reflect.DeepEqual(rows, want) || !ended {
		t.Fatalf("rows %+v, ended %v", rows, ended)
	}
}

func TestOnlyControlCallsCanUseMQTT(t *testing.T) {
	for _, c := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "/api/site/channels", true},
		{"GET", "/api/site/entrance/recordings?date=2026-11-08", true},
		{"POST", "/api/site/entrance/ptz", true},
		{"GET", "/api/site/entrance/relay", false},
		{"GET", "/api/site/entrance/replay?at=123", false},
		{"GET", "http://127.0.0.1:5000/api/site/channels", false},
	} {
		if got := isRPC(c.method, c.path); got != c.allowed {
			t.Errorf("%s %s: got %t", c.method, c.path, got)
		}
	}
}
