package capture

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
)

type Event struct {
	At      string `json:"at"`
	Message string `json:"message"`
}
type Status struct {
	Running    bool    `json:"running"`
	Registered bool    `json:"registered"`
	Streaming  bool    `json:"streaming"`
	Encoder    string  `json:"encoder"`
	Frames     uint64  `json:"frames"`
	Bytes      uint64  `json:"bytes"`
	Error      string  `json:"error"`
	Events     []Event `json:"events"`
}
type mediaSession struct {
	dialog *sipgo.DialogServerSession
	cancel context.CancelFunc
	done   chan struct{}
}

// sipgo adds the listening socket to its shared transport pool before its first
// read. Registration must wait for that point, otherwise its client opens a
// second socket on the same local port and Windows rejects the duplicate bind.
type readyPacketConn struct {
	net.PacketConn
	ready chan struct{}
	once  sync.Once
}

func (c *readyPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.once.Do(func() { close(c.ready) })
	return c.PacketConn.ReadFrom(p)
}

type Device struct {
	op      sync.Mutex
	mu      sync.Mutex
	config  Config
	path    string
	status  Status
	cancel  context.CancelFunc
	done    chan struct{}
	session *mediaSession
	serial  atomic.Uint32
}

func NewDevice(path string) (*Device, error) {
	c, e := LoadConfig(path)
	return &Device{config: c, path: path}, e
}
func (d *Device) Snapshot() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.status
	s.Events = append([]Event{}, s.Events...)
	return s
}
func (d *Device) Config() Config {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.config
	c.Password = ""
	return c
}
func (d *Device) Save(c Config) error {
	if c.Codec == "" {
		c.Codec = "h264"
	}
	if err := c.validateVideo(); err != nil {
		return err
	}
	if c.BitrateMode == "auto" {
		c.MaxBitrate = recommendedMaxBitrate(c)
	}
	d.op.Lock()
	defer d.op.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.status.Running {
		return errors.New("请先停止设备，再修改配置")
	}
	if c.Password == "" {
		c.Password = d.config.Password
	}
	if err := SaveConfig(d.path, c); err != nil {
		return err
	}
	d.config = c
	return nil
}
func (d *Device) note(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.Contains(msg, "password") || strings.Contains(msg, "Authorization") {
		return
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	d.status.Events = append(d.status.Events, Event{time.Now().Format("15:04:05"), msg})
	if len(d.status.Events) > 80 {
		d.status.Events = d.status.Events[len(d.status.Events)-80:]
	}
}
func (d *Device) failure(err error) {
	d.mu.Lock()
	d.status.Error = err.Error()
	d.mu.Unlock()
	d.note(err.Error())
}
func (d *Device) Start() error {
	d.op.Lock()
	defer d.op.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.status.Running {
		return nil
	}
	c := d.config
	if err := c.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.done = make(chan struct{})
	d.status = Status{Running: true, Events: []Event{}}
	go func() {
		defer close(d.done)
		err := d.run(ctx, c)
		d.mu.Lock()
		d.status.Running = false
		d.status.Registered = false
		d.status.Streaming = false
		d.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			d.failure(err)
		}
	}()
	return nil
}
func (d *Device) Stop() {
	d.op.Lock()
	defer d.op.Unlock()
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
func (d *Device) run(ctx context.Context, c Config) error {
	remote, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(c.Server, strconv.Itoa(c.Port)))
	if err != nil {
		return err
	}
	local := c.LocalIP
	if local == "" {
		conn, e := net.DialUDP("udp4", nil, remote)
		if e != nil {
			return e
		}
		local = conn.LocalAddr().(*net.UDPAddr).IP.String()
		conn.Close()
	}
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(local), Port: c.SIPPort})
	if err != nil {
		return err
	}
	defer socket.Close()
	ua, err := sipgo.NewUA(sipgo.WithUserAgent("ViewHub Capture"))
	if err != nil {
		return err
	}
	defer ua.Close()
	client, err := sipgo.NewClient(ua, sipgo.WithClientHostname(local), sipgo.WithClientPort(c.SIPPort), sipgo.WithClientConnectionAddr(socket.LocalAddr().String()), sipgo.WithClientNAT())
	if err != nil {
		return err
	}
	server, err := sipgo.NewServer(ua)
	if err != nil {
		return err
	}
	contact := sip.ContactHeader{Address: sip.Uri{Scheme: "sip", User: c.DeviceID, Host: local, Port: c.SIPPort}}
	dialogs := sipgo.DialogUA{Client: client, ContactHDR: contact, RewriteContact: true}
	authorized := func(req *sip.Request) bool {
		host, p, e := net.SplitHostPort(req.Source())
		return e == nil && net.ParseIP(host).Equal(remote.IP) && p == strconv.Itoa(c.Port) && req.From() != nil && req.From().Address.User == c.ServerID
	}
	reply := func(req *sip.Request, tx sip.ServerTransaction, n int, reason string) {
		_ = tx.Respond(sip.NewResponseFromRequest(req, n, reason, nil))
	}
	request := func(method sip.RequestMethod, body string) *sip.Request {
		req := sip.NewRequest(method, sip.Uri{Scheme: "sip", User: c.ServerID, Host: c.Realm})
		req.SetTransport("UDP")
		req.SetDestination(remote.String())
		params := sip.NewParams()
		params.Add("tag", fmt.Sprintf("capture-%d", d.serial.Add(1)))
		req.AppendHeader(&sip.FromHeader{Address: sip.Uri{Scheme: "sip", User: c.DeviceID, Host: c.Realm}, Params: params})
		req.AppendHeader(sip.HeaderClone(&contact))
		if method == sip.REGISTER {
			req.AppendHeader(&sip.ToHeader{Address: sip.Uri{Scheme: "sip", User: c.DeviceID, Host: c.Realm}})
		} else {
			req.AppendHeader(sip.NewHeader("Content-Type", "Application/MANSCDP+xml"))
		}
		req.SetBody([]byte(body))
		return req
	}
	sendXML := func(body string) error {
		timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		res, e := client.Do(timeout, request(sip.MESSAGE, body))
		if e == nil && res.StatusCode != 200 {
			e = fmt.Errorf("平台 MESSAGE 返回 %d", res.StatusCode)
		}
		return e
	}
	server.OnMessage(func(req *sip.Request, tx sip.ServerTransaction) {
		if !authorized(req) {
			reply(req, tx, 403, "Forbidden")
			return
		}
		var q struct {
			Command string `xml:"CmdType"`
			SN      int    `xml:"SN"`
			ID      string `xml:"DeviceID"`
		}
		if xml.Unmarshal(req.Body(), &q) != nil {
			reply(req, tx, 400, "Invalid XML")
			return
		}
		reply(req, tx, 200, "OK")
		if q.ID != c.DeviceID && q.ID != c.ChannelID {
			return
		}
		head := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?><Response><CmdType>%s</CmdType><SN>%d</SN><DeviceID>%s</DeviceID>", q.Command, q.SN, q.ID)
		var body string
		switch q.Command {
		case "Catalog":
			body = head + fmt.Sprintf("<SumNum>1</SumNum><DeviceList Num=\"1\"><Item><DeviceID>%s</DeviceID><Name>USB Camera</Name><Manufacturer>ViewHub</Manufacturer><Model>Capture</Model><Owner>Local</Owner><CivilCode>%s</CivilCode><Parental>0</Parental><ParentID>%s</ParentID><SafetyWay>0</SafetyWay><RegisterWay>1</RegisterWay><Secrecy>0</Secrecy><Status>ON</Status><Info><PTZType>3</PTZType></Info></Item></DeviceList></Response>", c.ChannelID, c.Realm[:6], c.DeviceID)
		case "DeviceInfo":
			body = head + "<Result>OK</Result><DeviceName>ViewHub Capture</DeviceName><Manufacturer>ViewHub</Manufacturer><Model>USB</Model><Firmware>0.1.0</Firmware><Channel>1</Channel></Response>"
		case "DeviceStatus":
			body = head + "<Result>OK</Result><Online>ONLINE</Online><Status>OK</Status></Response>"
		case "RecordInfo":
			body = head + "<Name>USB Camera</Name><SumNum>0</SumNum><RecordList Num=\"0\"></RecordList></Response>"
		default:
			return
		}
		if e := sendXML(body); e != nil && ctx.Err() == nil {
			d.failure(e)
		}
	})
	server.OnInvite(func(req *sip.Request, tx sip.ServerTransaction) {
		if !authorized(req) || req.Recipient.User != c.ChannelID {
			reply(req, tx, 403, "Forbidden")
			return
		}
		o, e := parseOffer(string(req.Body()))
		if e != nil || !net.ParseIP(o.IP).Equal(remote.IP) {
			reply(req, tx, 488, "Unsupported SDP")
			return
		}
		d.mu.Lock()
		if d.session != nil || ctx.Err() != nil {
			d.mu.Unlock()
			reply(req, tx, 486, "Busy")
			return
		}
		dialog, e := dialogs.ReadInvite(req, tx)
		if e != nil {
			d.mu.Unlock()
			reply(req, tx, 400, "Invalid INVITE")
			return
		}
		mediaCtx, cancel := context.WithCancel(ctx)
		// Terminate the pending INVITE on stop, including a platform that never ACKs.
		stopTransaction := context.AfterFunc(mediaCtx, tx.Terminate)
		ackDeadline := time.AfterFunc(8*time.Second, tx.Terminate)
		session := &mediaSession{dialog: dialog, cancel: cancel, done: make(chan struct{})}
		d.session = session
		d.mu.Unlock()
		defer func() {
			ackDeadline.Stop()
			stopTransaction()
			cancel()
			dialog.Close()
			d.mu.Lock()
			if d.session == session {
				d.session = nil
				d.status.Streaming = false
				d.status.Encoder = ""
			}
			d.mu.Unlock()
			close(session.done)
		}()
		sdp := fmt.Sprintf("v=0\r\no=%s 0 0 IN IP4 %s\r\ns=Play\r\nc=IN IP4 %s\r\nt=0 0\r\nm=video 9 RTP/AVP %d\r\na=sendonly\r\na=rtpmap:%d PS/90000\r\ny=%010d\r\n", c.DeviceID, local, local, o.PT, o.PT, o.SSRC)
		if e = dialog.RespondSDP([]byte(sdp)); e != nil {
			if ctx.Err() == nil {
				d.failure(e)
			}
			return
		}
		ackDeadline.Stop()
		if dialog.LoadState() != sip.DialogStateConfirmed {
			d.failure(errors.New("点播未收到有效 ACK"))
			return
		}
		if mediaCtx.Err() != nil {
			return
		}
		d.note("平台已点播，开始采集 USB 视频")
		e = stream(mediaCtx, c, o, func(n int) {
			d.mu.Lock()
			d.status.Streaming = true
			d.status.Frames++
			d.status.Bytes += uint64(n)
			d.status.Error = ""
			d.mu.Unlock()
		}, func(name string) {
			d.mu.Lock()
			d.status.Encoder = name
			d.mu.Unlock()
		}, d.note)
		if mediaCtx.Err() == nil {
			if e == nil {
				e = errors.New("摄像头采集已结束")
			}
			d.failure(e)
			byeCtx, byeCancel := context.WithTimeout(ctx, 2*time.Second)
			_ = dialog.Bye(byeCtx)
			byeCancel()
		}
	})
	server.OnAck(func(req *sip.Request, tx sip.ServerTransaction) {
		if !authorized(req) {
			return
		}
		d.mu.Lock()
		s := d.session
		d.mu.Unlock()
		if s != nil && req.CallID() != nil && s.dialog.InviteRequest.CallID().Value() == req.CallID().Value() {
			_ = s.dialog.ReadAck(req, tx)
		}
	})
	server.OnBye(func(req *sip.Request, tx sip.ServerTransaction) {
		if !authorized(req) {
			reply(req, tx, 403, "Forbidden")
			return
		}
		d.mu.Lock()
		s := d.session
		d.mu.Unlock()
		if s == nil || s.dialog.ReadBye(req, tx) != nil {
			reply(req, tx, 481, "Dialog Not Found")
			return
		}
		s.cancel()
		d.note("平台停止点播，释放摄像头")
	})
	server.OnOptions(func(req *sip.Request, tx sip.ServerTransaction) {
		if !authorized(req) {
			reply(req, tx, 403, "Forbidden")
			return
		}
		reply(req, tx, 200, "OK")
	})
	serveErr := make(chan error, 1)
	listener := &readyPacketConn{PacketConn: socket, ready: make(chan struct{})}
	go func() { serveErr <- server.ServeUDP(listener) }()
	select {
	case <-listener.ready:
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		return nil
	}
	defer func() {
		d.mu.Lock()
		s := d.session
		d.mu.Unlock()
		if s != nil {
			s.cancel()
			if s.dialog.LoadState() == sip.DialogStateConfirmed {
				byeCtx, byeCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = s.dialog.Bye(byeCtx)
				byeCancel()
			}
			s.dialog.Close()
		}
		socket.Close()
		ua.Close()
		if s != nil {
			<-s.done
		}
	}()
	register := func(expires int) error {
		r := request(sip.REGISTER, "")
		r.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(expires)))
		timeout, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		res, e := client.Do(timeout, r)
		if e != nil {
			return e
		}
		if res.StatusCode == 401 {
			res, e = client.DoDigestAuth(timeout, r, res, sipgo.DigestAuth{Username: c.DeviceID, Password: c.Password})
		}
		if e == nil && res.StatusCode != 200 {
			e = fmt.Errorf("注册失败：SIP %d", res.StatusCode)
		}
		return e
	}
	d.note("正在向平台注册")
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var registered bool
	var nextRegister, nextHeartbeat time.Time
	lostMedia := func() {
		d.mu.Lock()
		session := d.session
		d.mu.Unlock()
		if session != nil {
			session.cancel()
			d.note("平台连接中断，释放旧媒体会话，等待恢复后重新点播")
		}
	}
	for {
		if !registered || time.Now().After(nextRegister) {
			e := register(3600)
			registered = e == nil
			d.mu.Lock()
			d.status.Registered = registered
			if registered {
				d.status.Error = ""
			}
			d.mu.Unlock()
			if e != nil {
				lostMedia()
				d.failure(e)
			} else {
				nextRegister = time.Now().Add(25 * time.Minute)
				d.note("注册成功，等待平台点播")
			}
		}
		if registered && time.Now().After(nextHeartbeat) {
			sn := d.serial.Add(1)
			e := sendXML(fmt.Sprintf("<?xml version=\"1.0\"?><Notify><CmdType>Keepalive</CmdType><SN>%d</SN><DeviceID>%s</DeviceID><Status>OK</Status></Notify>", sn, c.DeviceID))
			nextHeartbeat = time.Now().Add(30 * time.Second)
			if e != nil {
				registered = false
				d.mu.Lock()
				d.status.Registered = false
				d.mu.Unlock()
				lostMedia()
				d.failure(e)
			}
		}
		select {
		case <-ctx.Done():
			_ = register(0)
			return nil
		case e := <-serveErr:
			return e
		case <-ticker.C:
		}
	}
}
