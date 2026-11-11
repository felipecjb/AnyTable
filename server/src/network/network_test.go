package network

import (
	"bufio"
	"bytes"
	"image/jpeg"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"anytable/common/protocol"
	"anytable/server/src/capture"
	"anytable/server/src/input"
	"anytable/server/src/stream"
)

type recBackend struct {
	mu    sync.Mutex
	moves [][2]int
	keys  []uint16
}

func (r *recBackend) MoveTo(_ capture.Region, x, y int) error {
	r.mu.Lock()
	r.moves = append(r.moves, [2]int{x, y})
	r.mu.Unlock()
	return nil
}
func (r *recBackend) Button(protocol.MouseButton, bool) error { return nil }
func (r *recBackend) Wheel(int) error                         { return nil }
func (r *recBackend) Key(c uint16, down bool) error {
	r.mu.Lock()
	if down {
		r.keys = append(r.keys, c)
	}
	r.mu.Unlock()
	return nil
}
func (r *recBackend) Close() error { return nil }
func (r *recBackend) Name() string { return "rec" }
func (r *recBackend) snapshot() ([][2]int, []uint16) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]int(nil), r.moves...), append([]uint16(nil), r.keys...)
}

type testBackend struct {
	inj    *input.Injector
	region capture.Region
}

func (b *testBackend) Injector() *input.Injector      { return b.inj }
func (b *testBackend) Region() (capture.Region, bool) { return b.region, true }

type regionFn func() capture.Region

func (f regionFn) Region() capture.Region { return f() }

func startServer(t *testing.T, pin string) (*Server, *recBackend, *stream.Hub, string) {
	t.Helper()
	region := capture.Region{W: 200, H: 100, RootW: 200, RootH: 100}
	rec := &recBackend{}
	inj := input.NewWithBackend(rec, regionFn(func() capture.Region { return region }))
	hub := stream.NewHub()
	srv := New(hub, &testBackend{inj: inj, region: region}, nil, Options{Port: 0, PIN: pin, FPS: 30, Quality: 80})
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() { srv.Run(done); close(finished) }()
	t.Cleanup(func() { close(done); <-finished })

	deadline := time.Now().Add(2 * time.Second)
	for srv.Addr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("server did not bind")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, port, _ := net.SplitHostPort(srv.Addr())
	pn, _ := strconv.Atoi(port)

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() { // stand-in for the capture pump
		for {
			select {
			case <-stop:
				return
			case <-time.After(15 * time.Millisecond):
				pix := make([]byte, 200*100*4)
				f := capture.NewFrame(pix, 200, 100, true, nil)
				hub.Publish(f)
				f.Release()
			}
		}
	}()
	return srv, rec, hub, "127.0.0.1:" + strconv.Itoa(pn)
}

func dialAuth(t *testing.T, addr, pin string, w, h uint16) (net.Conn, *bufio.Reader, *protocol.AuthPacket) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	raw, err := protocol.NewAuthPacket(pin, w, h).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(raw); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c)
	reply, err := protocol.DecodeAuth(br)
	if err != nil {
		return c, br, nil
	}
	return c, br, reply
}

func TestHandshakeRejectsWrongPINAndAcceptsRightOne(t *testing.T) {
	_, _, _, addr := startServer(t, "4829")

	_, _, bad := dialAuth(t, addr, "0000", 1920, 1080)
	if bad == nil || bad.Authenticated {
		t.Fatalf("wrong PIN must be rejected, got %+v", bad)
	}
	_, _, ok := dialAuth(t, addr, "4829", 100, 100)
	if ok == nil || !ok.Authenticated {
		t.Fatalf("right PIN must be accepted, got %+v", ok)
	}
	if ok.Width != 100 || ok.Height != 50 { // 200x100 host fitted into a 100x100 viewport
		t.Fatalf("negotiated %dx%d, want 100x50", ok.Width, ok.Height)
	}
}

func TestStreamPingPongAndInput(t *testing.T) {
	srv, rec, _, addr := startServer(t, "")
	c, br, reply := dialAuth(t, addr, "", 200, 100)
	if reply == nil || !reply.Authenticated {
		t.Fatalf("auth failed: %+v", reply)
	}

	sentTs := protocol.NowMicroseconds() - 500
	ping, _ := (&protocol.PingPacket{Type: protocol.PING, SequenceID: 77, Timestamp: sentTs}).Serialize()
	mouse, _ := protocol.NewMouseInputPacket(0.5, 0.5, protocol.ButtonLeft, protocol.ButtonPress, 0).Serialize()
	key, _ := protocol.NewKeyboardInputPacket(0, 30, 0, protocol.KeyDown).Serialize()
	for _, b := range [][]byte{ping, mouse, key} {
		if _, err := c.Write(b); err != nil {
			t.Fatal(err)
		}
	}

	gotFrame, gotPong := false, false
	for !(gotFrame && gotPong) {
		hdr, err := br.Peek(3)
		if err != nil {
			t.Fatalf("read: %v (frame=%v pong=%v)", err, gotFrame, gotPong)
		}
		switch protocol.PacketType(hdr[2]) {
		case protocol.PacketTypeFrame:
			f, err := protocol.DecodeFrame(br)
			if err != nil {
				t.Fatal(err)
			}
			img, err := jpeg.Decode(bytes.NewReader(f.Payload))
			if err != nil || img.Bounds().Dx() != 200 || img.Bounds().Dy() != 100 {
				t.Fatalf("bad JPEG frame: %v %v", err, img)
			}
			gotFrame = true
		case protocol.PacketTypePong:
			p, err := protocol.DecodePing(br)
			if err != nil {
				t.Fatal(err)
			}
			if p.SequenceID != 77 || p.Timestamp != sentTs {
				t.Fatalf("PONG must echo seq+timestamp, got %+v", p)
			}
			gotPong = true
		default:
			if _, err := protocol.DecodePing(br); err != nil { // server-initiated PING
				t.Fatal(err)
			}
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		moves, keys := rec.snapshot()
		if len(moves) > 0 && len(keys) > 0 {
			// 0.5*(200-1)+0.5 = 100 ; 0.5*(100-1)+0.5 = 50
			if moves[0] != [2]int{100, 50} || keys[0] != 30 {
				t.Fatalf("unexpected injection: %v %v", moves, keys)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("input never injected: %v %v", moves, keys)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(srv.Clients()); n != 1 {
		t.Fatalf("Clients() = %d, want 1", n)
	}
}

func TestSecondAuthAndGarbageCloseConnection(t *testing.T) {
	_, _, _, addr := startServer(t, "")
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	// The server closes at once (possibly with RST because it left our bytes unread); only a timeout is a failure.
	if _, err := io.ReadAll(c); err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatalf("server kept garbage connection open: %v", err)
		}
	}
}

func TestBruteForceLockout(t *testing.T) {
	_, _, _, addr := startServer(t, "4829")
	for i := 0; i < lockoutFails; i++ {
		if _, _, r := dialAuth(t, addr, "1111", 100, 100); r == nil || r.Authenticated {
			t.Fatalf("attempt %d should be rejected with a reply", i)
		}
	}
	if _, _, r := dialAuth(t, addr, "4829", 100, 100); r != nil {
		t.Fatalf("locked-out IP must get no reply even with the right PIN, got %+v", r)
	}
}

func TestApplyRebindsPort(t *testing.T) {
	srv, _, _, _ := startServer(t, "")
	first := srv.Addr()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := net.SplitHostPort(probe.Addr().String())
	probe.Close()
	port, _ := strconv.Atoi(p)

	o := srv.Options()
	o.Port = port
	if err := srv.Apply(o); err != nil {
		t.Fatal(err)
	}
	if srv.Addr() == first {
		t.Fatal("listener did not move")
	}
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+p, time.Second)
	if err != nil {
		t.Fatalf("new port not reachable: %v", err)
	}
	c.Close()

	busy, _ := net.Listen("tcp", ":0")
	defer busy.Close()
	_, bp, _ := net.SplitHostPort(busy.Addr().String())
	o.Port, _ = strconv.Atoi(bp)
	if err := srv.Apply(o); err == nil {
		t.Fatal("binding a busy port must fail")
	}
	if srv.Options().Port != port {
		t.Fatal("failed Apply must leave options unchanged")
	}
}
