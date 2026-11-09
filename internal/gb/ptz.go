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
	lock, _ := controls.LoadOrStore(key, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
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
