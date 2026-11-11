// Package gui is the settings/tray front-end. It runs as a separate process from the streaming
// daemon and only talks to it over the loopback admin API, so GUI work can never delay capture or network I/O.
//
// Pure Go (no CGO, keeps the static glibc-2.27-safe build): a StatusNotifier tray icon plus a local web UI
// shown in a browser app window. Closing the window leaves only the tray icon; nothing polls while the
// window is closed or hidden (the page stops its timer on visibilitychange), so the GUI idles at 0 FPS.
package gui

import (
	"bytes"
	"context"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"runtime"
	"time"

	"fyne.io/systray"
)

//go:embed ui.html
var uiHTML []byte

// Options configure the GUI process.
type Options struct {
	AdminAddr string // daemon admin API, e.g. 127.0.0.1:18443
	WebAddr   string // where this process serves the UI, e.g. 127.0.0.1:18444
	Show      bool   // open the window immediately instead of tray-only
	Log       *log.Logger
}

// Run shows the tray icon and serves the UI until ctx ends or the user quits from the tray.
// If another GUI instance already owns WebAddr, it just opens that instance's window.
func Run(ctx context.Context, o Options) error {
	pageURL := "http://" + o.WebAddr + "/"
	ln, err := net.Listen("tcp", o.WebAddr)
	if err != nil {
		openWindow(pageURL)
		return nil
	}
	target, err := url.Parse("http://" + o.AdminAddr)
	if err != nil {
		return err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, `{"error":"engine not reachable"}`, http.StatusBadGateway)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", proxy)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(uiHTML)
	})
	srv := &http.Server{Handler: guardLocal(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	systray.Run(func() { onReady(o, pageURL) }, func() {})
	return nil
}

func onReady(o Options, pageURL string) {
	systray.SetIcon(icon())
	systray.SetTitle("AnyTable")
	systray.SetTooltip("AnyTable server")
	systray.SetOnTapped(func() { openWindow(pageURL) })
	open := systray.AddMenuItem("Open settings", "Show the AnyTable settings window")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit tray (server keeps running)", "Close this tray icon only")
	go func() {
		for {
			select {
			case <-open.ClickedCh:
				openWindow(pageURL)
			case <-quit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
	if o.Show {
		openWindow(pageURL)
	}
}

// guardLocal rejects requests whose Host is not loopback (DNS-rebinding protection).
func guardLocal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// openWindow prefers a Chromium-family app window (no tabs/URL bar); falls back to the default browser.
func openWindow(pageURL string) {
	for _, b := range []string{"google-chrome", "chromium-browser", "chromium", "microsoft-edge"} {
		if p, err := exec.LookPath(b); err == nil {
			start(exec.Command(p, "--app="+pageURL))
			return
		}
	}
	switch runtime.GOOS {
	case "windows":
		start(exec.Command("rundll32", "url.dll,FileProtocolHandler", pageURL))
	case "darwin":
		start(exec.Command("open", pageURL))
	default:
		start(exec.Command("xdg-open", pageURL))
	}
}

func start(c *exec.Cmd) {
	if c.Start() == nil {
		go func() { _ = c.Wait() }() // reap the child
	}
}

// icon renders a 32x32 PNG (blue disc with a white ring) so no binary asset has to ship.
func icon() []byte {
	const n = 32
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)-15.5, float64(y)-15.5
			d := dx*dx + dy*dy
			switch {
			case d <= 6*6:
				img.Set(x, y, color.RGBA{255, 255, 255, 255})
			case d <= 10*10:
				img.Set(x, y, color.RGBA{0x1a, 0x73, 0xe8, 255})
			case d <= 15*15:
				img.Set(x, y, color.RGBA{255, 255, 255, 255})
			case d <= 16*16:
				img.Set(x, y, color.RGBA{0x1a, 0x73, 0xe8, 255})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
