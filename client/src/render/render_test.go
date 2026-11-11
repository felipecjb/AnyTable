package render

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"strconv"
	"testing"
	"time"

	"anytable/client/src/hud"
	"anytable/common/protocol"
)

func TestFitRectAspectRatioAndStretch(t *testing.T) {
	// 16:9 remote inside 4:3 window -> letterbox top/bottom
	r := FitRect(1024, 768, 1920, 1080, false)
	if r.W != 1024 || int(r.H+0.5) != 576 || r.X != 0 || int(r.Y+0.5) != 96 {
		t.Fatalf("letterbox rect = %+v, want X=0 Y=96 W=1024 H=576", r)
	}

	// 4:3 remote inside 16:9 window -> pillarbox left/right
	r = FitRect(1600, 900, 800, 600, false)
	if int(r.W+0.5) != 1200 || r.H != 900 || int(r.X+0.5) != 200 || r.Y != 0 {
		t.Fatalf("pillarbox rect = %+v, want X=200 Y=0 W=1200 H=900", r)
	}

	// Stretch fills entire canvas
	r = FitRect(1024, 768, 1920, 1080, true)
	if r.X != 0 || r.Y != 0 || r.W != 1024 || r.H != 768 {
		t.Fatalf("stretch rect = %+v, want full 1024x768", r)
	}
}

func TestClientConnectReceivesFrameAndRepliesToPing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	pongCh := make(chan *protocol.PingPacket, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		auth, err := protocol.DecodeAuth(conn)
		if err != nil || auth.VerifyHandshake("1234") != nil {
			return
		}
		ack, _ := (&protocol.AuthPacket{
			Version:       protocol.ProtocolVersion,
			Width:         64,
			Height:        48,
			Authenticated: true,
		}).Serialize()
		_, _ = conn.Write(ack)

		// Send one red JPEG frame.
		img := image.NewRGBA(image.Rect(0, 0, 64, 48))
		for y := 0; y < 48; y++ {
			for x := 0; x < 64; x++ {
				img.SetRGBA(x, y, color.RGBA{220, 20, 20, 255})
			}
		}
		var jb bytes.Buffer
		_ = jpeg.Encode(&jb, img, &jpeg.Options{Quality: 90})
		fp, _ := protocol.NewFramePacket(1, protocol.EncodingJPEG, jb.Bytes()).Serialize()
		_, _ = conn.Write(fp)

		// Send a server PING and expect an immediate PONG back.
		sp, _ := protocol.NewPingPacket(42).Serialize()
		_, _ = conn.Write(sp)

		for {
			pkt, err := protocol.DecodePing(conn)
			if err != nil {
				return
			}
			if pkt.Type == protocol.PacketTypePong && pkt.SequenceID == 42 {
				pongCh <- pkt
				return
			}
		}
	}()

	frameReady := make(chan struct{}, 1)
	tel := hud.NewTelemetry()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cl, err := Connect(ctx, ConnectConfig{
		Host: "127.0.0.1", Port: port, PIN: "1234", ViewportW: 1280, ViewportH: 720,
	}, tel, func() {
		select {
		case frameReady <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer cl.Close()

	select {
	case <-frameReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for decoded JPEG frame")
	}

	df := cl.LatestFrame()
	if df == nil || df.W != 64 || df.H != 48 || df.Pix[0] < 180 {
		t.Fatalf("unexpected decoded frame: %+v", df)
	}

	select {
	case p := <-pongCh:
		if p.SequenceID != 42 {
			t.Fatalf("unexpected PONG seq %d", p.SequenceID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not answer server PING with PONG")
	}
}
