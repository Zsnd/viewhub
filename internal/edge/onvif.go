package edge

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/icholy/digest"
)

const searchNS = "http://www.onvif.org/ver10/search/wsdl"
const replayNS = "http://www.onvif.org/ver10/replay/wsdl"
const deviceNS = "http://www.onvif.org/ver10/device/wsdl"
const ptzNS = "http://www.onvif.org/ver20/ptz/wsdl"

type onvifConfig struct {
	Device, Username, Password, Recording, Track, Profile string
	PTZ                                                   bool
	StrictHost                                            bool
}

func esc(v string) string { return html.EscapeString(v) }

func (c *onvifConfig) soap(ctx context.Context, target, ns, action, body string, dst any) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errUnsupported
	}
	nonce := randomBytes(20)
	created := time.Now().UTC().Format(time.RFC3339)
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(c.Password))
	security := fmt.Sprintf(`<wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"><wsse:UsernameToken><wsse:Username>%s</wsse:Username><wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">%s</wsse:Password><wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">%s</wsse:Nonce><wsu:Created>%s</wsu:Created></wsse:UsernameToken></wsse:Security>`, esc(c.Username), base64.StdEncoding.EncodeToString(h.Sum(nil)), base64.StdEncoding.EncodeToString(nonce), created)
	raw := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tt="http://www.onvif.org/ver10/schema" xmlns:x="` + ns + `"><s:Header>` + security + `</s:Header><s:Body><x:` + action + ">" + body + "</x:" + action + "></s:Body></s:Envelope>"
	req, err := http.NewRequestWithContext(ctx, "POST", target, strings.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", `application/soap+xml; charset=utf-8; action="`+ns+"/"+action+`"`)
	client := &http.Client{Transport: &digest.Transport{Username: c.Username, Password: c.Password}, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var envelope struct {
		Body struct {
			Inner []byte `xml:",innerxml"`
		}
	}
	if err := xml.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&envelope); err != nil {
		if res.StatusCode == 404 || res.StatusCode == 501 {
			return errUnsupported
		}
		return err
	}
	dec := xml.NewDecoder(bytes.NewReader(envelope.Body.Inner))
	for {
		tok, err := dec.Token()
		if err != nil {
			return unavailable
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if el.Name.Local == "Fault" {
			var fault struct {
				Inner string `xml:",innerxml"`
			}
			dec.DecodeElement(&fault, &el)
			if strings.Contains(fault.Inner, "ActionNotSupported") || strings.Contains(fault.Inner, "NotImplemented") {
				return errUnsupported
			}
			return unavailable
		}
		if res.StatusCode != 200 {
			return unavailable
		}
		if el.Name.Local != action+"Response" {
			return unavailable
		}
		if dst == nil {
			return nil
		}
		return dec.DecodeElement(dst, &el)
	}
}

func (c *onvifConfig) service(ctx context.Context, ns string) (string, error) {
	var result struct {
		Services []struct{ Namespace, XAddr string } `xml:"Service"`
	}
	if err := c.soap(ctx, c.Device, deviceNS, "GetServices", `<x:IncludeCapability>false</x:IncludeCapability>`, &result); err != nil {
		return "", err
	}
	for _, s := range result.Services {
		if s.Namespace != ns {
			continue
		}
		u, err := url.Parse(s.XAddr)
		device, _ := url.Parse(c.Device)
		if err != nil || u.Host == "" || u.User != nil {
			return "", errUnsupported
		}
		u.Host = device.Host
		u.Scheme = device.Scheme
		return u.String(), nil
	}
	return "", errUnsupported
}

func (c *onvifConfig) recordings(ctx context.Context, start, end time.Time) ([]interval, error) {
	if c.Recording == "" {
		return nil, errUnsupported
	}
	target, err := c.service(ctx, searchNS)
	if err != nil {
		return nil, err
	}
	trackToken, err := c.videoTrack(ctx, target)
	if err != nil {
		return nil, err
	}
	if _, err := c.replayURI(ctx); err != nil {
		return nil, err
	}
	var search struct{ SearchToken string }
	body := fmt.Sprintf(`<x:StartPoint>%s</x:StartPoint><x:EndPoint>%s</x:EndPoint><x:Scope><tt:IncludedRecordings>%s</tt:IncludedRecordings></x:Scope><x:SearchFilter><wsnt:TopicExpression xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2" xmlns:tns1="http://www.onvif.org/ver10/topics" Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">tns1:RecordingHistory/Track/State</wsnt:TopicExpression></x:SearchFilter><x:IncludeStartState>true</x:IncludeStartState><x:KeepAliveTime>PT10S</x:KeepAliveTime>`, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), esc(c.Recording))
	if err := c.soap(ctx, target, searchNS, "FindEvents", body, &search); err != nil {
		return nil, err
	}
	if search.SearchToken == "" {
		return nil, unavailable
	}
	token := "<x:SearchToken>" + esc(search.SearchToken) + "</x:SearchToken>"
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c.soap(ctx, target, searchNS, "EndSearch", token, nil)
	}()
	type item struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:"Value,attr"`
	}
	var states []trackState
	for page := 0; page < 64; page++ {
		var result struct {
			List struct {
				State   string `xml:"SearchState"`
				Results []struct {
					RecordingToken, TrackToken string
					Time                       time.Time
					Topic                      string `xml:"Event>Topic"`
					Source                     []item `xml:"Event>Message>Message>Source>SimpleItem"`
					Data                       []item `xml:"Event>Message>Message>Data>SimpleItem"`
				} `xml:"Result"`
			} `xml:"ResultList"`
		}
		if err := c.soap(ctx, target, searchNS, "GetEventSearchResults", token+`<x:MinResults>1</x:MinResults><x:MaxResults>100</x:MaxResults><x:WaitTime>PT1S</x:WaitTime>`, &result); err != nil {
			return nil, err
		}
		for _, event := range result.List.Results {
			if !strings.HasSuffix(event.Topic, "RecordingHistory/Track/State") || event.Time.IsZero() {
				continue
			}
			recording, track := event.RecordingToken, event.TrackToken
			for _, v := range event.Source {
				if v.Name == "RecordingToken" || v.Name == "Recording" {
					recording = v.Value
				}
				if v.Name == "Track" || v.Name == "TrackToken" {
					track = v.Value
				}
			}
			if recording != c.Recording || track != trackToken {
				continue
			}
			for _, v := range event.Data {
				if v.Name == "IsDataPresent" {
					states = append(states, trackState{event.Time.Unix(), v.Value == "true" || v.Value == "1"})
				}
			}
		}
		if result.List.State == "Completed" {

			return trackIntervals(states, start.Unix(), end.Unix()), nil
		}
	}
	return nil, unavailable
}

var errTrackRequired = errors.New("multiple ONVIF video tracks require a track token")

func (c *onvifConfig) videoTrack(ctx context.Context, target string) (string, error) {
	if c.Track != "" {
		return c.Track, nil
	}
	var result struct {
		Recording struct {
			Tracks []struct{ TrackToken, TrackType string } `xml:"Track"`
		} `xml:"RecordingInformation"`
	}
	if err := c.soap(ctx, target, searchNS, "GetRecordingInformation", "<x:RecordingToken>"+esc(c.Recording)+"</x:RecordingToken>", &result); err != nil {
		return "", err
	}
	var token string
	for _, track := range result.Recording.Tracks {
		if track.TrackType != "Video" || track.TrackToken == "" {
			continue
		}
		if token != "" && token != track.TrackToken {
			return "", errTrackRequired
		}
		token = track.TrackToken
	}
	if token == "" {
		return "", errUnsupported
	}
	return token, nil
}

type trackState struct {
	at     int64
	active bool
}

func trackIntervals(states []trackState, start, end int64) []interval {
	sort.SliceStable(states, func(i, j int) bool { return states[i].at < states[j].at })
	rows := []interval{}
	var open int64
	for _, state := range states {
		at := max(start, min(end, state.at))
		if state.active && open == 0 {
			open = at
		}
		if !state.active && open != 0 {
			rows = append(rows, interval{Start: open, End: at, ProfileG: true})
			open = 0
		}
	}
	if open != 0 && open < end {
		rows = append(rows, interval{Start: open, End: end, ProfileG: true})
	}
	return rows
}

func (c *onvifConfig) replayURI(ctx context.Context) (string, error) {
	target, err := c.service(ctx, replayNS)
	if err != nil {
		return "", err
	}
	var result struct {
		URI string `xml:"Uri"`
	}
	body := `<x:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream><tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport></x:StreamSetup><x:RecordingToken>` + esc(c.Recording) + `</x:RecordingToken>`
	if err := c.soap(ctx, target, replayNS, "GetReplayUri", body, &result); err != nil {
		return "", err
	}
	uri, err := url.Parse(result.URI)
	if err != nil || uri.Hostname() == "" || (uri.Scheme != "rtsp" && uri.Scheme != "rtsps") {
		return "", errUnsupported
	}
	if c.StrictHost {
		device, _ := url.Parse(c.Device)
		if uri.Hostname() != device.Hostname() {
			return "", errUnsupported
		}
	}
	if result.URI == "" {
		return "", errUnsupported
	}
	return result.URI, nil
}

func (c *onvifConfig) move(ctx context.Context, action, preset string) error {
	if c.Profile == "" {
		return errUnsupported
	}
	profile := "<x:ProfileToken>" + esc(c.Profile) + "</x:ProfileToken>"
	var op, body string
	switch action {
	case "stop":
		op, body = "Stop", profile+"<x:PanTilt>true</x:PanTilt><x:Zoom>true</x:Zoom>"
	case "preset":
		if len(preset) < 1 || len(preset) > 128 {
			return errors.New("invalid preset")
		}
		op, body = "GotoPreset", profile+"<x:PresetToken>"+esc(preset)+"</x:PresetToken>"
	default:
		velocity := map[string]string{"up": `<tt:PanTilt x="0" y="0.4"/>`, "down": `<tt:PanTilt x="0" y="-0.4"/>`, "left": `<tt:PanTilt x="-0.4" y="0"/>`, "right": `<tt:PanTilt x="0.4" y="0"/>`, "in": `<tt:Zoom x="0.4"/>`, "out": `<tt:Zoom x="-0.4"/>`}[action]
		if velocity == "" {
			return errors.New("invalid action")
		}
		op, body = "ContinuousMove", profile+"<x:Velocity>"+velocity+"</x:Velocity><x:Timeout>PT1S</x:Timeout>"
	}
	target, err := c.service(ctx, ptzNS)
	if err != nil {
		return err
	}
	return c.soap(ctx, target, ptzNS, op, body, nil)
}
