// anytable-client: hardware-accelerated remote desktop viewer with HUD, input capture, and tray persistence.
package main

import (
	"bytes"
	"context"
	"flag"
	"image"
	"log"
	"math"
	"os"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/goregular"

	"anytable/client/src/hud"
	"anytable/client/src/input"
	"anytable/client/src/launcher"
	"anytable/client/src/render"
)

type appState uint8

const (
	stateLauncher appState = iota
	stateConnecting
	stateConnected
)

type connectResult struct {
	req    launcher.ConnectRequest
	client *render.Client
	tel    *hud.Telemetry
	err    error
}

type viewerApp struct {
	state      appState
	store      *launcher.Store
	launchUI   *launcher.UI
	canvas     *render.Canvas
	overlay    *hud.Overlay
	capturer   *input.Capturer
	tray       *trayController
	stretch    bool
	lastRect   input.CanvasRect
	lastFPSMod ebiten.FPSModeType

	mu         sync.Mutex
	client     *render.Client
	tel        *hud.Telemetry
	connRes    chan connectResult
	connCancel context.CancelFunc

	screenW int
	screenH int
	scale   float64
}

func newViewerApp(host string, port int, pin string, autoConnect bool) (*viewerApp, error) {
	faceSrc, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		return nil, err
	}
	store, err := launcher.Load(launcher.DefaultPath())
	if err != nil {
		store, _ = launcher.Load("")
	}
	lui := launcher.NewUI(faceSrc, store)
	lui.SetPrefill(host, port, pin)

	app := &viewerApp{
		state:      stateLauncher,
		store:      store,
		launchUI:   lui,
		canvas:     render.NewCanvas(),
		overlay:    hud.NewOverlay(faceSrc),
		capturer:   input.NewCapturer(),
		tray:       startTray(),
		lastFPSMod: ebiten.FPSModeVsyncOn,
		screenW:    1024,
		screenH:    640,
		scale:      1,
	}

	if autoConnect && host != "" {
		if port <= 0 {
			port = launcher.DefaultPort
		}
		app.beginConnect(launcher.ConnectRequest{Host: host, Port: port, PIN: pin})
	}
	return app, nil
}

func (a *viewerApp) beginConnect(req launcher.ConnectRequest) {
	if a.connCancel != nil {
		a.connCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.connCancel = cancel
	a.state = stateConnecting
	a.launchUI.SetStatus("Connecting to "+req.Host+"...", false, true)
	ch := make(chan connectResult, 1)
	a.connRes = ch

	vw, vh := uint16(1920), uint16(1080)
	if a.screenW > 0 && a.screenW <= 65535 {
		vw = uint16(a.screenW)
	}
	if a.screenH > 0 && a.screenH <= 65535 {
		vh = uint16(a.screenH)
	}

	go func() {
		tel := hud.NewTelemetry()
		cl, err := render.Connect(ctx, render.ConnectConfig{
			Host:      req.Host,
			Port:      req.Port,
			PIN:       req.PIN,
			ViewportW: vw,
			ViewportH: vh,
		}, tel, func() {
			if !ebiten.IsWindowMinimized() {
				ebiten.ScheduleFrame()
			}
		})
		ch <- connectResult{req: req, client: cl, tel: tel, err: err}
		ebiten.ScheduleFrame()
	}()
}

func (a *viewerApp) disconnect(reason string, isErr bool) {
	if a.connCancel != nil {
		a.connCancel()
		a.connCancel = nil
	}
	a.mu.Lock()
	cl := a.client
	a.client = nil
	a.tel = nil
	a.mu.Unlock()
	if cl != nil {
		a.capturer.ReleaseAll(func(raw []byte, urgent bool) {
			cl.Send(raw, urgent)
		})
		cl.Close()
	}
	a.canvas.Reset()
	a.state = stateLauncher
	ebiten.SetFullscreen(false)
	ebiten.SetWindowTitle("AnyTable")
	a.launchUI.SetStatus(reason, isErr, false)
	a.syncFPSMode()
}

func (a *viewerApp) syncFPSMode() {
	want := ebiten.FPSModeVsyncOffMinimum
	if a.state == stateConnected && !ebiten.IsWindowMinimized() {
		want = ebiten.FPSModeVsyncOn
	}
	if want != a.lastFPSMod {
		ebiten.SetFPSMode(want)
		a.lastFPSMod = want
		ebiten.SetRunnableOnUnfocused(a.state == stateConnected && !ebiten.IsWindowMinimized())
	}
}

func (a *viewerApp) Update() error {
	a.syncFPSMode()

	// Drain tray menu actions
	for {
		select {
		case cmd := <-a.tray.cmds:
			switch cmd {
			case trayCmdShow:
				ebiten.RestoreWindow()
				a.syncFPSMode()
				ebiten.ScheduleFrame()
			case trayCmdDisconnect:
				if a.state != stateLauncher {
					a.disconnect("Disconnected.", false)
				}
			case trayCmdQuit:
				a.disconnect("", false)
				a.tray.stop()
				return ebiten.Termination
			}
		default:
			goto afterTray
		}
	}
afterTray:

	if ebiten.IsWindowBeingClosed() {
		if a.state == stateConnected || a.state == stateConnecting {
			// Background persistence: closing window minimizes/hides to tray at 0 FPS while preserving session.
			if a.client != nil {
				a.capturer.ReleaseAll(func(raw []byte, urgent bool) {
					a.client.Send(raw, urgent)
				})
			}
			ebiten.MinimizeWindow()
			a.syncFPSMode()
			return nil
		}
		a.disconnect("", false)
		a.tray.stop()
		return ebiten.Termination
	}

	// Poll async dial/handshake completion
	if a.state == stateConnecting && a.connRes != nil {
		select {
		case res := <-a.connRes:
			a.connRes = nil
			if res.err != nil {
				a.state = stateLauncher
				a.launchUI.SetStatus("Connection failed: "+res.err.Error(), true, false)
			} else {
				_ = a.store.Add(res.req.Host, res.req.Port)
				a.mu.Lock()
				a.client = res.client
				a.tel = res.tel
				a.mu.Unlock()
				a.state = stateConnected
				a.launchUI.SetStatus("", false, false)
				ebiten.SetWindowTitle("AnyTable - " + res.req.Host)
				a.syncFPSMode()
			}
		default:
		}
	}

	if a.state != stateConnected {
		if req := a.launchUI.Update(a.screenW, a.screenH, a.scale); req != nil {
			a.beginConnect(*req)
		}
		return nil
	}

	// Connected state
	cl := a.client
	if cl == nil {
		a.state = stateLauncher
		return nil
	}
	if err := cl.Err(); err != nil {
		ebiten.RestoreWindow()
		a.disconnect("Session ended: "+err.Error(), true)
		return nil
	}

	// When minimized/hidden in tray, drop all GUI/input work to idle (0 FPS).
	if ebiten.IsWindowMinimized() {
		return nil
	}

	imx, imy := ebiten.CursorPosition()
	mx, my := float64(imx), float64(imy)
	clicked := inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
	stats := a.tel.Snapshot()

	action := a.overlay.Update(a.screenW, a.scale, mx, my, clicked, stats, a.stretch, ebiten.IsFullscreen())
	switch action {
	case hud.ActionToggleStretch:
		a.stretch = !a.stretch
	case hud.ActionToggleFullscreen:
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
	case hud.ActionCtrlAltDel:
		for _, pkt := range input.CtrlAltDelPackets() {
			cl.Send(pkt, true)
		}
	case hud.ActionDisconnect:
		a.disconnect("Disconnected.", false)
		return nil
	}

	hudBlocks := a.overlay.ConsumedMouse(mx, my)
	a.capturer.Update(a.lastRect, hudBlocks, ebiten.IsFocused(), func(raw []byte, urgent bool) {
		cl.Send(raw, urgent)
	})
	return nil
}

func (a *viewerApp) Draw(screen *ebiten.Image) {
	if ebiten.IsWindowMinimized() {
		return
	}
	if a.state != stateConnected {
		a.launchUI.Draw(screen, a.scale)
		return
	}
	cl := a.client
	if cl == nil {
		return
	}
	rw, rh := cl.RemoteSize()
	a.lastRect = a.canvas.Draw(screen, cl.LatestFrame(), rw, rh, a.stretch)
	a.overlay.Draw(screen, a.scale, a.tel.Snapshot(), a.stretch, ebiten.IsFullscreen())
}

func (a *viewerApp) LayoutF(outsideWidth, outsideHeight float64) (float64, float64) {
	dsf := 1.0
	if m := ebiten.Monitor(); m != nil {
		dsf = m.DeviceScaleFactor()
	}
	if dsf < 1 {
		dsf = 1
	}
	pw := math.Round(outsideWidth * dsf)
	ph := math.Round(outsideHeight * dsf)
	if pw < 1 {
		pw = 1
	}
	if ph < 1 {
		ph = 1
	}
	a.screenW = int(pw)
	a.screenH = int(ph)
	a.scale = dsf
	return pw, ph
}

func (a *viewerApp) Layout(outsideWidth, outsideHeight int) (int, int) {
	return outsideWidth, outsideHeight
}

func main() {
	var (
		host = flag.String("host", "", "server IP or hostname")
		port = flag.Int("port", launcher.DefaultPort, "server TCP port")
		pin  = flag.String("pin", "", "optional server PIN")
		auto = flag.Bool("autoconnect", false, "connect immediately on launch")
	)
	flag.Parse()

	ebiten.SetWindowTitle("AnyTable")
	ebiten.SetWindowSize(1024, 640)
	ebiten.SetWindowSizeLimits(480, 420, -1, -1)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowClosingHandled(true)
	ebiten.SetWindowIcon([]image.Image{appIconImage()})
	ebiten.SetFPSMode(ebiten.FPSModeVsyncOffMinimum)

	app, err := newViewerApp(*host, *port, *pin, *auto)
	if err != nil {
		log.New(os.Stderr, "anytable-client: ", log.LstdFlags).Fatalf("init failed: %v", err)
	}
	if err := ebiten.RunGame(app); err != nil {
		log.New(os.Stderr, "anytable-client: ", log.LstdFlags).Fatalf("runtime error: %v", err)
	}
	app.disconnect("", false)
	app.tray.stop()
}
