package edge

import (
	"context"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/icholy/digest"
)

type hikConfig struct {
	Origin, Username, Password string
	Track                      int
}

func (c *hikConfig) recordings(ctx context.Context, start, end time.Time) ([]interval, error) {
	base, err := url.Parse(c.Origin)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || c.Track < 1 {
		return nil, errUnsupported
	}
	target := base.JoinPath("ISAPI", "ContentMgmt", "search")
	client := &http.Client{Timeout: 8 * time.Second, Transport: &digest.Transport{Username: c.Username, Password: c.Password}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	rows := []interval{}
	id := hex.EncodeToString(randomBytes(16))
	for page, offset := 0, 0; page < 100; page++ {
		raw := fmt.Sprintf(`<CMSearchDescription version="1.0" xmlns="http://www.hikvision.com/ver20/XMLSchema"><searchID>%s</searchID><trackList><trackID>%d</trackID></trackList><timeSpanList><timeSpan><startTime>%s</startTime><endTime>%s</endTime></timeSpan></timeSpanList><maxResults>100</maxResults><searchResultPostion>%d</searchResultPostion><metadataList><metadataDescriptor>//recordType.meta.std-cgi.com</metadataDescriptor></metadataList></CMSearchDescription>`, id, c.Track, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), offset)
		req, _ := http.NewRequestWithContext(ctx, "POST", target.String(), strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/xml")
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var answer struct {
			Status  string `xml:"responseStatusStrg"`
			Matches []struct {
				Track int       `xml:"trackID"`
				Start time.Time `xml:"timeSpan>startTime"`
				End   time.Time `xml:"timeSpan>endTime"`
				URI   string    `xml:"mediaSegmentDescriptor>playbackURI"`
			} `xml:"matchList>searchMatchItem"`
		}
		err = xml.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&answer)
		res.Body.Close()
		if res.StatusCode == 404 || res.StatusCode == 501 {
			return nil, errUnsupported
		}
		if err != nil || res.StatusCode != 200 {
			return nil, unavailable
		}
		switch answer.Status {
		case "OK", "MORE", "NO MATCHES":
		default:
			return nil, unavailable
		}
		for _, item := range answer.Matches {
			if item.Track != c.Track || item.URI == "" {
				continue
			}
			rows = append(rows, interval{Start: max(item.Start.Unix(), start.Unix()), End: min(item.End.Unix(), end.Unix()), URI: item.URI})
		}
		if answer.Status != "MORE" {
			return rows, nil
		}
		if len(answer.Matches) == 0 {
			return nil, unavailable
		}
		offset += len(answer.Matches)
	}
	return nil, unavailable
}
