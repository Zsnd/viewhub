package gb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type controlKey struct{ device, channel string }

var controls sync.Map

type controlState struct {
	mu         sync.Mutex
	timer      *time.Timer
	generation uint64
}

func (s *controlState) cancelLease() {
	s.generation++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *controlState) hold(ctx context.Context, command string, send func(context.Context, string) error, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.cancelLease()
	if err := send(ctx, command); err != nil {
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		code, _ := commandBytes("stop", 0)
		return errors.Join(err, send(stop, code))
	}
	generation := s.generation
	s.timer = time.AfterFunc(ttl, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if generation != s.generation {
			return
		}
		s.cancelLease()
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		code, _ := commandBytes("stop", 0)
		_ = send(stop, code)
	})
	return nil
}

func commandBytes(action string, preset int) (string, error) {
	raw := [8]byte{0xa5, 0x0f, 0x01}
	switch action {
	case "stop":
	case "left":
		raw[3], raw[4] = 2, 64
	case "right":
		raw[3], raw[4] = 1, 64
	case "up":
		raw[3], raw[5] = 8, 64
	case "down":
		raw[3], raw[5] = 4, 64
	case "in":
		raw[3], raw[6] = 16, 64
	case "out":
		raw[3], raw[6] = 32, 64
	case "preset":
		if preset < 1 || preset > 255 {
			return "", errors.New("invalid preset")
		}
		raw[3], raw[5] = 0x82, byte(preset)
	default:
		return "", errors.New("invalid PTZ action")
	}
	for _, b := range raw[:7] {
		raw[7] += b
	}
	return strings.ToUpper(fmt.Sprintf("%x", raw)), nil
}

func (g *Gateway) Control(ctx context.Context, device, channel, action string, preset int) error {
	return g.control(ctx, device, channel, action, preset, false)
}

func (g *Gateway) ControlHold(ctx context.Context, device, channel, action string) error {
	if action != "up" && action != "down" && action != "left" && action != "right" {
		return errors.New("invalid held PTZ action")
	}
	return g.control(ctx, device, channel, action, 0, true)
}

func (g *Gateway) control(ctx context.Context, device, channel, action string, preset int, held bool) error {
	cmd, err := commandBytes(action, preset)
	if err != nil {
		return err
	}
	if !ValidCode(device) || !ValidCode(channel) {
		return errors.New("invalid device channel")
	}
	send := func(ctx context.Context, code string) error {
		sn := g.serial.Add(1)
		body := fmt.Sprintf("<Control><CmdType>DeviceControl</CmdType><SN>%d</SN><DeviceID>%s</DeviceID><PTZCmd>%s</PTZCmd><Info><ControlPriority>5</ControlPriority></Info></Control>", sn, channel, code)
		return g.sendXML(ctx, device, body)
	}
	key := controlKey{device, channel}
	value, _ := controls.LoadOrStore(key, &controlState{})
	state := value.(*controlState)
	if held {
		return state.hold(ctx, cmd, send, 900*time.Millisecond)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	state.cancelLease()
	if action == "stop" || action == "preset" {
		return send(ctx, cmd)
	}
	// A stop must be sent even if a caller disappears after the movement was delivered.
	err = send(ctx, cmd)
	if err == nil {
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}
	stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stopCmd, _ := commandBytes("stop", 0)
	stopErr := send(stop, stopCmd)
	return errors.Join(err, stopErr)
}
