// Package render implements the asynchronous remote frame receiver, JPEG decoder,
// and hardware-accelerated Ebitengine canvas renderer with aspect-ratio fitting.
package render

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"anytable/client/src/hud"
	"anytable/common/protocol"
)

const (
	dialTimeout  = 5 * time.Second
	readTimeout  = 15 * time.Second
	writeTimeout = 3 * time.Second
	urgentCap    = 256
)

var (
	// ErrAuthRejected is returned when the server rejects the handshake PIN or display isn't ready.
	ErrAuthRejected = errors.New("authentication rejected or remote display unavailable")
)

// ConnectConfig defines connection parameters sent during the initial handshake.
type ConnectConfig struct {
	Host      string
	Port      int
	PIN       string
	ViewportW uint16
	ViewportH uint16
}

// DecodedFrame holds one decoded RGBA frame ready for GPU upload on the render thread.
type DecodedFrame struct {
	Pix []byte
	W   int
	H   int
	Seq uint64
}

// Client manages an active remote session over TCP completely off the GUI/render thread.
type Client struct {
	conn    net.Conn
	tel     *hud.Telemetry
	remoteW int
	remoteH int
	onFrame func()

	ctx    context.Context
	cancel context.CancelFunc

	// 1-slot freshest raw JPEG mailbox (drops stale undecoded JPEGs if decoder lags).
	rawMu    sync.Mutex
	rawBytes []byte
	rawSeq   uint64
	rawReady chan struct{}

	// Latest decoded RGBA frame for the render thread.
	decMu    sync.Mutex
	decFrame *DecodedFrame

	// Outgoing write queue: urgent packets (PONG, clicks, keys) + coalesced latest mouse move.
	urgentCh chan []byte
	moveMu   sync.Mutex
	movePkt  []byte
	writeSig chan struct{}

	errMu   sync.Mutex
	lastErr error
	wg      sync.WaitGroup
	closed  atomic.Bool
}

// Connect dials the server, completes the protocol handshake, and starts async I/O + decode loops.
func Connect(parent context.Context, cfg ConnectConfig, tel *hud.Telemetry, onFrame func()) (*Client, error) {
	vw, vh := cfg.ViewportW, cfg.ViewportH
	if vw == 0 {
		vw = 1920
	}
	if vh == 0 {
		vh = 1080
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(parent, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}

	authRaw, err := protocol.NewAuthPacket(cfg.PIN, vw, vh).Serialize()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := conn.Write(authRaw); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(dialTimeout))
	br := bufio.NewReaderSize(conn, 64<<10)
	reply, err := protocol.DecodeAuth(br)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake failed: %w", err)
	}
	if !reply.Authenticated {
		_ = conn.Close()
		return nil, ErrAuthRejected
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})

	ctx, cancel := context.WithCancel(parent)
	c := &Client{
		conn:     conn,
		tel:      tel,
		remoteW:  int(reply.Width),
		remoteH:  int(reply.Height),
		onFrame:  onFrame,
		ctx:      ctx,
		cancel:   cancel,
		rawReady: make(chan struct{}, 1),
		urgentCh: make(chan []byte, urgentCap),
		writeSig: make(chan struct{}, 1),
	}

	c.wg.Add(4)
	go func() { defer c.wg.Done(); c.readLoop(br) }()
	go func() { defer c.wg.Done(); c.decodeLoop() }()
	go func() { defer c.wg.Done(); c.writeLoop() }()
	go func() {
		defer c.wg.Done()
		c.tel.Run(c.ctx, func(raw []byte) error {
			c.Send(raw, true)
			return nil
		})
	}()
	return c, nil
}

// RemoteSize returns the negotiated host display dimensions.
func (c *Client) RemoteSize() (int, int) {
	return c.remoteW, c.remoteH
}

// LatestFrame returns the most recently decoded RGBA frame, or nil if none has arrived yet.
func (c *Client) LatestFrame() *DecodedFrame {
	c.decMu.Lock()
	defer c.decMu.Unlock()
	return c.decFrame
}

// Send enqueues a serialized protocol packet without blocking the caller thread.
func (c *Client) Send(raw []byte, urgent bool) {
	if c.closed.Load() || len(raw) == 0 {
		return
	}
	if urgent {
		select {
		case c.urgentCh <- raw:
			c.signalWrite()
		default:
		}
		return
	}
	c.moveMu.Lock()
	c.movePkt = raw
	c.moveMu.Unlock()
	c.signalWrite()
}

func (c *Client) signalWrite() {
	select {
	case c.writeSig <- struct{}{}:
	default:
	}
}

// Err returns the disconnect reason if the session ended abnormally.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.lastErr
}

func (c *Client) fail(err error) {
	if err != nil {
		c.errMu.Lock()
		if c.lastErr == nil {
			c.lastErr = err
		}
		c.errMu.Unlock()
	}
	c.Close()
}

// Close terminates the session and stops all background goroutines.
func (c *Client) Close() {
	if c.closed.Swap(true) {
		return
	}
	c.cancel()
	_ = c.conn.Close()
	c.wg.Wait()
}

func (c *Client) readLoop(br *bufio.Reader) {
	for {
		if c.ctx.Err() != nil {
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(readTimeout))
		hdr, err := br.Peek(3)
		if err != nil {
			c.fail(err)
			return
		}
		switch protocol.PacketType(hdr[2]) {
		case protocol.PacketTypeFrame:
			pkt, err := protocol.DecodeFrame(br)
			if err != nil {
				c.fail(err)
				return
			}
			c.tel.RecordPacket(protocol.FrameHeaderSize+len(pkt.Payload), true)
			if pkt.Encoding == protocol.EncodingJPEG && len(pkt.Payload) > 0 {
				c.pushRawFrame(pkt.Payload, pkt.SequenceID)
			}
		case protocol.PacketTypePing:
			pkt, err := protocol.DecodePing(br)
			if err != nil {
				c.fail(err)
				return
			}
			c.tel.RecordPacket(protocol.PingPacketSize, false)
			if pongRaw, err := protocol.NewPongPacket(pkt.SequenceID, pkt.Timestamp).Serialize(); err == nil {
				c.Send(pongRaw, true)
			}
		case protocol.PacketTypePong:
			pkt, err := protocol.DecodePing(br)
			if err != nil {
				c.fail(err)
				return
			}
			c.tel.RecordPacket(protocol.PingPacketSize, false)
			c.tel.OnPong(pkt)
		default:
			c.fail(fmt.Errorf("unexpected packet type %#x", hdr[2]))
			return
		}
	}
}

func (c *Client) pushRawFrame(jpegBytes []byte, seq uint64) {
	c.rawMu.Lock()
	c.rawBytes = jpegBytes
	c.rawSeq = seq
	c.rawMu.Unlock()
	select {
	case c.rawReady <- struct{}{}:
	default:
	}
}

func (c *Client) decodeLoop() {
	var workImg *image.RGBA
	var frontPix, backPix []byte
	useFront := true

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.rawReady:
		}

		c.rawMu.Lock()
		raw := c.rawBytes
		seq := c.rawSeq
		c.rawBytes = nil
		c.rawMu.Unlock()
		if len(raw) == 0 {
			continue
		}

		img, err := jpeg.Decode(bytes.NewReader(raw))
		if err != nil {
			continue
		}
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		if w <= 0 || h <= 0 {
			continue
		}

		need := w * h * 4
		if workImg == nil || workImg.Rect.Dx() != w || workImg.Rect.Dy() != h {
			workImg = image.NewRGBA(image.Rect(0, 0, w, h))
		}
		draw.Draw(workImg, workImg.Bounds(), img, b.Min, draw.Src)

		var dst []byte
		if useFront {
			if cap(frontPix) < need {
				frontPix = make([]byte, need)
			}
			frontPix = frontPix[:need]
			dst = frontPix
		} else {
			if cap(backPix) < need {
				backPix = make([]byte, need)
			}
			backPix = backPix[:need]
			dst = backPix
		}
		useFront = !useFront
		copy(dst, workImg.Pix[:need])

		c.decMu.Lock()
		c.decFrame = &DecodedFrame{Pix: dst, W: w, H: h, Seq: seq}
		c.decMu.Unlock()

		if c.onFrame != nil {
			c.onFrame()
		}
	}
}

func (c *Client) writeLoop() {
	for {
		if err := c.flushPending(); err != nil {
			c.fail(err)
			return
		}
		select {
		case <-c.ctx.Done():
			return
		case <-c.writeSig:
		}
	}
}

func (c *Client) flushPending() error {
	for {
		select {
		case raw := <-c.urgentCh:
			if err := c.writeOne(raw); err != nil {
				return err
			}
		default:
			c.moveMu.Lock()
			mv := c.movePkt
			c.movePkt = nil
			c.moveMu.Unlock()
			if mv == nil {
				return nil
			}
			if err := c.writeOne(mv); err != nil {
				return err
			}
		}
	}
}

func (c *Client) writeOne(raw []byte) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := c.conn.Write(raw)
	return err
}
