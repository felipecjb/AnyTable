// Package network is the non-blocking TCP transmitter: handshake/auth, frame streaming,
// input receive, and ping/pong telemetry.
package network

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"anytable/common/protocol"
	"anytable/server/src/capture"
	"anytable/server/src/input"
	"anytable/server/src/stream"
)

const (
	maxConns        = 32
	authTimeout     = 5 * time.Second
	idleTimeout     = 15 * time.Second
	writeTimeout    = 3 * time.Second
	pingInterval    = time.Second
	rebindInterval  = 2 * time.Second
	sendBufferBytes = 256 << 10 // small kernel buffer: saturation shows up fast, so stale frames get dropped
	lockoutFails    = 5
	lockoutFor      = time.Minute
)

// Backend is what the network layer needs from the host engine.
type Backend interface {
	Injector() *input.Injector      // nil while no display is available
	Region() (capture.Region, bool) // false while no display is available
}

// Options are the live-tunable server settings.
type Options struct {
	Port    int
	PIN     string
	FPS     int
	Quality int
}

// ClientInfo describes a connected, authenticated client.
type ClientInfo struct {
	Addr  string `json:"addr"`
	RTTUs int64  `json:"rtt_us"` // server-measured round trip; -1 until the first PONG
}

type failState struct {
	n     int
	until time.Time
}

// Server accepts clients and streams frames from the hub.
type Server struct {
	hub *stream.Hub
	be  Backend
	log *log.Logger

	mu        sync.RWMutex
	opts      Options
	ln        net.Listener
	boundPort int
	bindErr   error
	sessions  map[*session]struct{}

	conns  int32
	failMu sync.Mutex
	fails  map[string]*failState
}

// New creates a server; call Run to start listening.
func New(hub *stream.Hub, be Backend, lg *log.Logger, opts Options) *Server {
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	return &Server{
		hub: hub, be: be, log: lg, opts: opts, boundPort: -1,
		sessions: map[*session]struct{}{}, fails: map[string]*failState{},
	}
}

// Options returns the current settings.
func (s *Server) Options() Options {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.opts
}

// Addr returns the bound listen address ("" when not listening).
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// BindError returns the last listen failure ("" when bound as requested).
func (s *Server) BindError() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bindErr == nil {
		return ""
	}
	return s.bindErr.Error()
}

// Apply installs new options. A port change binds the new port first and only then drops the old
// listener (existing sessions keep running); on bind failure nothing changes.
func (s *Server) Apply(o Options) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o.Port != s.boundPort {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", o.Port))
		if err != nil {
			s.bindErr = err
			return err
		}
		old := s.ln
		s.ln, s.boundPort, s.bindErr = ln, o.Port, nil
		go s.acceptLoop(ln)
		if old != nil {
			_ = old.Close()
		}
	}
	s.opts = o
	return nil
}

// Run keeps the listener bound (retrying every 2s if the port is busy, e.g. at boot) until ctx ends.
func (s *Server) Run(done <-chan struct{}) {
	if err := s.Apply(s.Options()); err != nil {
		s.log.Printf("listen failed, will retry: %v", err)
	}
	t := time.NewTicker(rebindInterval)
	defer t.Stop()
	for {
		select {
		case <-done:
			s.shutdown()
			return
		case <-t.C:
			o := s.Options()
			s.mu.RLock()
			bound := s.boundPort == o.Port && s.ln != nil
			s.mu.RUnlock()
			if !bound {
				if err := s.Apply(o); err == nil {
					s.log.Printf("listening on port %d", o.Port)
				}
			}
		}
	}
}

func (s *Server) shutdown() {
	s.mu.Lock()
	ln := s.ln
	s.ln, s.boundPort = nil, -1
	list := make([]*session, 0, len(s.sessions))
	for se := range s.sessions {
		list = append(list, se)
	}
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	for _, se := range list {
		_ = se.conn.Close()
	}
}

// Clients lists authenticated clients sorted by address.
func (s *Server) Clients() []ClientInfo {
	s.mu.RLock()
	out := make([]ClientInfo, 0, len(s.sessions))
	for se := range s.sessions {
		out = append(out, ClientInfo{Addr: se.conn.RemoteAddr().String(), RTTUs: atomic.LoadInt64(&se.rttUs)})
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out
}

func (s *Server) acceptLoop(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if atomic.AddInt32(&s.conns, 1) > maxConns {
			atomic.AddInt32(&s.conns, -1)
			_ = c.Close()
			continue
		}
		go func() {
			defer atomic.AddInt32(&s.conns, -1)
			s.handle(c)
		}()
	}
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetWriteBuffer(sendBufferBytes)
	}
	ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	if s.locked(ip) {
		return
	}

	_ = c.SetReadDeadline(time.Now().Add(authTimeout))
	pkt, err := readPacket(c)
	if err != nil || pkt.auth == nil {
		return
	}
	opts := s.Options()
	if err := pkt.auth.VerifyHandshake(opts.PIN); err != nil {
		if errors.Is(err, protocol.ErrAuthFailed) {
			s.recordFailure(ip)
		}
		s.replyAuth(c, pkt.auth.Width, pkt.auth.Height, false)
		return
	}
	region, ok := s.be.Region()
	if !ok {
		s.replyAuth(c, pkt.auth.Width, pkt.auth.Height, false) // host display not up yet; client retries
		return
	}
	s.clearFailures(ip)

	w, h := stream.FitSize(region.W, region.H, int(pkt.auth.Width), int(pkt.auth.Height))
	if !s.replyAuth(c, uint16(w), uint16(h), true) {
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	se := &session{srv: s, conn: c, maxW: int(pkt.auth.Width), maxH: int(pkt.auth.Height), rttUs: -1}
	s.mu.Lock()
	s.sessions[se] = struct{}{}
	s.mu.Unlock()
	s.log.Printf("client %s connected (%dx%d)", c.RemoteAddr(), w, h)
	se.run()
	s.mu.Lock()
	delete(s.sessions, se)
	s.mu.Unlock()
	s.log.Printf("client %s disconnected", c.RemoteAddr())
}

func (s *Server) replyAuth(c net.Conn, w, h uint16, ok bool) bool {
	if w == 0 || h == 0 {
		w, h = 1, 1
	}
	raw, err := (&protocol.AuthPacket{Version: protocol.ProtocolVersion, Width: w, Height: h, Authenticated: ok}).Serialize()
	if err != nil {
		return false
	}
	_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err = c.Write(raw)
	return err == nil
}

func (s *Server) locked(ip string) bool {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	f := s.fails[ip]
	return f != nil && time.Now().Before(f.until)
}

func (s *Server) recordFailure(ip string) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	if len(s.fails) > 1024 {
		s.fails = map[string]*failState{}
	}
	f := s.fails[ip]
	if f == nil {
		f = &failState{}
		s.fails[ip] = f
	}
	f.n++
	if f.n >= lockoutFails {
		f.n = 0
		f.until = time.Now().Add(lockoutFor)
	}
}

func (s *Server) clearFailures(ip string) {
	s.failMu.Lock()
	delete(s.fails, ip)
	s.failMu.Unlock()
}
