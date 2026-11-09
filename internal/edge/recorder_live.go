package edge

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const mediaNS = "http://www.onvif.org/ver10/media/wsdl"

type recorderProfile struct {
	Token   string                       `json:"token" xml:"token,attr"`
	Name    string                       `json:"name" xml:"Name"`
	Video   struct{ SourceToken string } `json:"-" xml:"VideoSourceConfiguration"`
	Encoder struct {
		Encoding   string
		Resolution struct{ Width, Height int }
	} `json:"-" xml:"VideoEncoderConfiguration"`
	PTZ *struct {
		Token string `xml:"token,attr"`
	} `json:"-" xml:"PTZConfiguration"`
}

func (c *onvifConfig) mediaProfiles(ctx context.Context) (string, []recorderProfile, error) {
	target, err := c.service(ctx, mediaNS)
	if err != nil {
		return "", nil, err
	}
	var out struct{ Profiles []recorderProfile }
	if err = c.soap(ctx, target, mediaNS, "GetProfiles", "", &out); err != nil {
		return "", nil, err
	}
	profiles := []recorderProfile{}
	for _, profile := range out.Profiles {
		if profile.Token != "" && len(profile.Token) <= 128 && profile.Video.SourceToken != "" {
			profiles = append(profiles, profile)
		}
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		a, b := profiles[i].Encoder, profiles[j].Encoder
		if strings.EqualFold(a.Encoding, "H264") != strings.EqualFold(b.Encoding, "H264") {
			return strings.EqualFold(a.Encoding, "H264")
		}
		return int64(a.Resolution.Width)*int64(a.Resolution.Height) > int64(b.Resolution.Width)*int64(b.Resolution.Height)
	})
	return target, profiles, nil
}

func (r recorderRecording) liveProfiles(profiles []recorderProfile) []recorderProfile {
	out := []recorderProfile{}
	for _, p := range profiles {
		if p.Video.SourceToken == r.Source.VideoSourceToken || (r.Source.VideoSourceToken == "" && p.Video.SourceToken == r.Source.ID) {
			out = append(out, p)
		}
	}
	return out
}

func (c *onvifConfig) streamURI(ctx context.Context, target, profile string) (string, error) {
	var out struct {
		MediaURI struct {
			URI string `xml:"Uri"`
		} `xml:"MediaUri"`
	}
	body := `<x:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream><tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport></x:StreamSetup><x:ProfileToken>` + esc(profile) + `</x:ProfileToken>`
	if err := c.soap(ctx, target, mediaNS, "GetStreamUri", body, &out); err != nil {
		return "", err
	}
	targetURL, err := url.Parse(out.MediaURI.URI)
	device, _ := url.Parse(c.Device)
	if err != nil || targetURL.Hostname() != device.Hostname() || (targetURL.Scheme != "rtsp" && targetURL.Scheme != "rtsps") || targetURL.Fragment != "" {
		return "", errUnsupported
	}
	targetURL.User = url.UserPassword(c.Username, c.Password)
	return targetURL.String(), nil
}

func (e *edge) configureLive(ctx context.Context, name, source string, check bool) error {
	target := e.rtc.JoinPath("api", "streams")
	if check {
		target.RawQuery = url.Values{"src": {name}}.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return err
		}
		res, err := upstreamClient.Do(req)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode == http.StatusOK {
			return nil
		}
	}
	target.RawQuery = url.Values{"name": {name}, "src": {source}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, target.String(), nil)
	if err != nil {
		return err
	}
	res, err := upstreamClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return unavailable
	}
	return nil
}
