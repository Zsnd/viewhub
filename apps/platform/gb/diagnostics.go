package gb

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const catalogTimeout = 12 * time.Second

type registerAttempt struct {
	Reason string
	At     time.Time
}
type catalogAttempt struct {
	SN       int
	State    string
	Deadline time.Time
	Total    int
	Items    map[string]Channel
}
type Diagnostics struct {
	Registration    string     `json:"registration"`
	Failure         string     `json:"failure,omitempty"`
	Catalog         string     `json:"catalog"`
	LastSignal      *time.Time `json:"lastSignal,omitempty"`
	LastAttempt     *time.Time `json:"lastAttempt,omitempty"`
	CatalogReceived int        `json:"catalogReceived,omitempty"`
	CatalogTotal    int        `json:"catalogTotal,omitempty"`
}

// Keep diagnostics for pre-registered device IDs only. A rejected request
// never changes the address or online status of an authenticated device.
func (g *Gateway) rememberRegister(id, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.attempts[id] = registerAttempt{Reason: reason, At: time.Now()}
}

func (g *Gateway) diagnostic(id string, now time.Time) (bool, Diagnostics) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := Diagnostics{Registration: "waiting", Catalog: "waiting"}
	r, registered := g.online[id]
	attempt, attempted := g.attempts[id]
	if attempted {
		d.LastAttempt = &attempt.At
	}
	online := registered && !now.After(r.expires) && now.Sub(r.seen) <= 190*time.Second
	switch {
	case online:
		d.Registration = "online"
	case attempted && attempt.Reason != "":
		d.Registration = "waiting"
		d.Failure = attempt.Reason
	case registered && now.After(r.expires):
		d.Registration = "offline"
		d.Failure = "registration_expired"
	case registered:
		d.Registration = "offline"
		d.Failure = "heartbeat_timeout"
	case attempted:
		d.Registration = "offline"
		d.Failure = "unregistered"
	}
	if registered {
		d.LastSignal = &r.seen
	}
	if c, ok := g.catalogs[id]; ok && online {
		d.Catalog = c.State
		d.CatalogReceived = len(c.Items)
		d.CatalogTotal = c.Total
		if c.State == "querying" && !now.Before(c.Deadline) {
			d.Catalog = "timeout"
		}
	}
	return online, d
}

func (g *Gateway) queryCatalog(id string) {
	now := time.Now()
	g.mu.Lock()
	current := g.catalogs[id]
	if current != nil && current.State == "querying" && now.Before(current.Deadline) {
		g.mu.Unlock()
		return
	}
	c := &catalogAttempt{SN: int(g.serial.Add(1)), State: "querying", Deadline: now.Add(catalogTimeout), Items: map[string]Channel{}}
	g.catalogs[id] = c
	g.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body := fmt.Sprintf("<Query><CmdType>Catalog</CmdType><SN>%d</SN><DeviceID>%s</DeviceID></Query>", c.SN, id)
	if err := g.sendXML(ctx, id, body); err != nil {
		g.mu.Lock()
		if g.catalogs[id] == c && c.State == "querying" && len(c.Items) == 0 {
			c.State = "request_failed"
		}
		g.mu.Unlock()
		slog.Warn("catalog request", "device", id, "error", err)
	}
}

func (g *Gateway) receiveCatalog(id string, msg message) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.catalogs[id]
	if c == nil || c.SN != msg.SN || (c.State != "querying" && c.State != "request_failed") {
		return
	}
	c.Total = max(c.Total, msg.Total, len(msg.Channels))
	for _, channel := range msg.Channels {
		if channel.ID != "" {
			c.Items[channel.ID] = channel
		}
	}
	if len(c.Items) < c.Total {
		return
	}
	channels := make([]Channel, 0, len(c.Items))
	for _, channel := range c.Items {
		if channel.Parental == 0 && ValidCode(channel.ID) {
			channels = append(channels, channel)
		}
	}
	if err := g.Store.saveChannels(id, channels); err != nil {
		c.State = "save_failed"
		slog.Error("save catalog", "device", id, "error", err)
		return
	}
	c.State = "ready"
}
