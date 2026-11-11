package network

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"anytable/common/protocol"
	"anytable/server/src/stream"
)

// session is one authenticated client: a frame sender, a 1 Hz pinger and a read loop.
type session struct {
	srv        *Server
	conn       net.Conn
	maxW, maxH int // client viewport; frames are downscaled to fit
	rttUs      int64
	pingSeq    uint64

	wmu          sync.Mutex // serializes writes: frames, PONGs and PINGs share one byte stream
	lastInjectEr time.Time
}

func (se *session) write(raw []byte) error {
	se.wmu.Lock()
	defer se.wmu.Unlock()
	_ = se.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := se.conn.Write(raw)
	return err
}

func (se *session) run() {
	slot := se.srv.hub.Subscribe()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); se.sendFrames(slot) }()
	go func() { defer wg.Done(); se.pingLoop(done) }()

	se.readLoop()

	close(done)
	_ = se.conn.Close()
	se.srv.hub.Unsubscribe(slot)
	wg.Wait()
	if inj := se.srv.be.Injector(); inj != nil {
		inj.ReleaseAll() // never leave a key or button stuck after a disconnect
	}
}

// sendFrames always sends the freshest frame: while a write is blocked on a saturated
// socket the slot keeps overwriting, so the backlog is discarded, never queued.
func (se *session) sendFrames(slot *stream.Slot) {
	enc := stream.NewEncoder(se.srv.Options().Quality)
	var seq uint64
	for {
		f, ok := slot.Take()
		if !ok {
			return
		}
		o := se.srv.Options()
		fps := o.FPS
		if fps < 1 {
			fps = 1
		}
		budget := time.Second / time.Duration(fps)
		enc.SetBaseQuality(o.Quality)

		start := time.Now()
		data, err := enc.Encode(f, se.maxW, se.maxH)
		ts := f.TimeUs
		f.Release()
		if err != nil {
			continue
		}
		seq++
		raw, err := (&protocol.FramePacket{
			SequenceID: seq, Timestamp: ts, Encoding: protocol.EncodingJPEG, Payload: data,
		}).Serialize()
		if err != nil {
			continue
		}
		if err := se.write(raw); err != nil {
			_ = se.conn.Close()
			return
		}
		enc.Adapt(time.Since(start), budget)
	}
}

func (se *session) pingLoop(done <-chan struct{}) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			raw, err := protocol.NewPingPacket(atomic.AddUint64(&se.pingSeq, 1)).Serialize()
			if err != nil || se.write(raw) != nil {
				_ = se.conn.Close()
				return
			}
		}
	}
}

func (se *session) readLoop() {
	for {
		_ = se.conn.SetReadDeadline(time.Now().Add(idleTimeout))
		pkt, err := readPacket(se.conn)
		if err != nil {
			return
		}
		switch {
		case pkt.ping != nil && pkt.ping.Type == protocol.PacketTypePing:
			// Immediate PONG echoing the original timestamp.
			raw, _ := protocol.NewPongPacket(pkt.ping.SequenceID, pkt.ping.Timestamp).Serialize()
			if se.write(raw) != nil {
				return
			}
		case pkt.ping != nil: // PONG answering our PING
			atomic.StoreInt64(&se.rttUs, int64(protocol.CalculateRTT(pkt.ping.Timestamp)/time.Microsecond))
		case pkt.input != nil:
			se.inject(pkt.input)
		default:
			return // a second AUTH is a protocol violation
		}
	}
}

func (se *session) inject(p *protocol.InputPacket) {
	inj := se.srv.be.Injector()
	if inj == nil {
		return
	}
	var err error
	switch p.Type {
	case protocol.PacketTypeInputMouse:
		err = inj.Mouse(p.Mouse)
	case protocol.PacketTypeInputKeyboard:
		err = inj.Key(p.Keyboard)
	}
	if err != nil && time.Since(se.lastInjectEr) > time.Second {
		se.lastInjectEr = time.Now()
		se.srv.log.Printf("input injection failed: %v", err)
	}
}
