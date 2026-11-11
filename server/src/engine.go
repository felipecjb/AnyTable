package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/jezek/xgb"

	"anytable/server/src/capture"
	"anytable/server/src/input"
	"anytable/server/src/network"
	"anytable/server/src/stream"
)

// Engine is the headless transmitter: display connection, capture pump, input injector and network server.
// It has no GUI dependency; the settings GUI talks to it over the local admin API.
type Engine struct {
	cfgPath string
	display string
	log     *log.Logger
	started time.Time

	hub *stream.Hub
	srv *network.Server
	dm  *DisplayManager

	mu       sync.RWMutex // guards everything below
	cfg      Config
	grab     *capture.X11Grabber
	inj      *input.Injector
	stopPump context.CancelFunc
	pumpDone chan struct{}
	ctx      context.Context
}

// NewEngine wires the components; Run starts them.
func NewEngine(cfgPath, display string, cfg Config, lg *log.Logger) *Engine {
	e := &Engine{cfgPath: cfgPath, display: display, cfg: cfg, log: lg, started: time.Now(), hub: stream.NewHub()}
	e.srv = network.New(e.hub, e, lg, serverOptions(cfg))
	e.dm = NewDisplayManager(display, e, lg)
	return e
}

func serverOptions(c Config) network.Options {
	return network.Options{Port: c.Port, PIN: c.PIN, FPS: c.FPSTarget, Quality: c.JPEGQuality}
}

// Config returns a copy of the live configuration.
func (e *Engine) Config() Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

// Run blocks until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	e.mu.Lock()
	e.ctx = ctx
	e.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); e.srv.Run(ctx.Done()) }()
	go func() { defer wg.Done(); e.dm.Run(ctx) }()
	go func() { defer wg.Done(); e.serveAdmin(ctx) }()
	wg.Wait()
}

// DisplayUp implements DisplayHandler: build capture + input on the fresh connection and start the pump.
func (e *Engine) DisplayUp(conn *xgb.Conn) error {
	g, err := capture.NewX11(conn)
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	inj, err := input.New(conn, g, e.log.Printf)
	if err != nil {
		g.Close()
		return err
	}
	e.log.Printf("capture mode %s, input backend %s, region %+v", g.Mode(), inj.Name(), g.Region())

	e.mu.Lock()
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	e.grab, e.inj, e.stopPump, e.pumpDone = g, inj, cancel, done
	e.mu.Unlock()
	go func() {
		defer close(done)
		e.runPump(ctx, g)
	}()
	return nil
}

// DisplayDown implements DisplayHandler: stop everything bound to the lost connection.
func (e *Engine) DisplayDown() {
	e.mu.Lock()
	g, inj, stop, done := e.grab, e.inj, e.stopPump, e.pumpDone
	e.grab, e.inj, e.stopPump, e.pumpDone = nil, nil, nil, nil
	e.mu.Unlock()
	if stop == nil {
		return
	}
	stop()
	<-done
	_ = inj.Close()
	g.Close()
}

func (e *Engine) runPump(ctx context.Context, g *capture.X11Grabber) {
	p := &stream.Pump{Hub: e.hub, FPS: func() int { return e.Config().FPSTarget }, Log: e.log}
	for ctx.Err() == nil {
		if err := p.Run(ctx, g); err != nil {
			e.log.Printf("capture pump stopped: %v", err)
			if !sleepCtx(ctx, displayPollInterval) {
				return
			}
		}
	}
}

// Injector implements network.Backend.
func (e *Engine) Injector() *input.Injector {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.inj
}

// Region implements network.Backend.
func (e *Engine) Region() (capture.Region, bool) {
	e.mu.RLock()
	g := e.grab
	e.mu.RUnlock()
	if g == nil {
		return capture.Region{}, false
	}
	return g.Region(), true
}

// SettingsUpdate is a partial settings change; nil fields stay as they are.
type SettingsUpdate struct {
	Port   *int    `json:"port"`
	PIN    *string `json:"pin"`
	Preset *string `json:"preset"`
}

// UpdateSettings validates, applies live (rebinding the port if needed) and persists the change.
func (e *Engine) UpdateSettings(u SettingsUpdate) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	prev, next := e.cfg, e.cfg
	if u.Port != nil {
		next.Port = *u.Port
	}
	if u.PIN != nil {
		next.PIN = *u.PIN
	}
	if u.Preset != nil {
		p, ok := PresetByName(*u.Preset)
		if !ok {
			return fmt.Errorf("unknown preset %q", *u.Preset)
		}
		next.FPSTarget, next.JPEGQuality = p.FPS, p.Quality
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if err := e.srv.Apply(serverOptions(next)); err != nil {
		return fmt.Errorf("cannot apply settings: %w", err)
	}
	if err := next.Save(e.cfgPath); err != nil {
		_ = e.srv.Apply(serverOptions(prev))
		return fmt.Errorf("cannot save %s: %w", e.cfgPath, err)
	}
	e.cfg = next
	return nil
}

// Status is the JSON snapshot shown by the GUI. It never contains the PIN.
type Status struct {
	Running   bool                 `json:"running"`
	DisplayUp bool                 `json:"display_up"`
	Display   string               `json:"display"`
	Input     string               `json:"input"`
	IPs       []string             `json:"ips"`
	Port      int                  `json:"port"`
	BindError string               `json:"bind_error"`
	Clients   []network.ClientInfo `json:"clients"`
	Preset    string               `json:"preset"`
	FPS       int                  `json:"fps"`
	Quality   int                  `json:"quality"`
	PINSet    bool                 `json:"pin_set"`
	UptimeSec int64                `json:"uptime_sec"`
}

// Status snapshots the engine state.
func (e *Engine) Status() Status {
	cfg := e.Config()
	up, _ := e.dm.State()
	e.mu.RLock()
	inj := e.inj
	e.mu.RUnlock()
	st := Status{
		Running: true, DisplayUp: up, Display: e.display, IPs: localIPv4s(), Port: cfg.Port,
		BindError: e.srv.BindError(), Clients: e.srv.Clients(), Preset: cfg.PresetName(),
		FPS: cfg.FPSTarget, Quality: cfg.JPEGQuality, PINSet: cfg.PIN != "",
		UptimeSec: int64(time.Since(e.started).Seconds()),
	}
	if inj != nil {
		st.Input = inj.Name()
	}
	return st
}

func localIPv4s() []string {
	ips := []string{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			ips = append(ips, n.IP.String())
		}
	}
	return ips
}
