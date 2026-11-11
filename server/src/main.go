// anytable-server: headless transmitter daemon (default) or its tray/settings GUI (-gui).
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/jezek/xgb"

	"anytable/server/src/gui"
)

const defaultConfigPath = "/etc/anytable/server.conf"

func main() {
	var (
		cfgPath = flag.String("config", envOr("ANYTABLE_CONFIG", defaultConfigPath), "path to anytable.conf")
		display = flag.String("display", envOr("DISPLAY", ":0"), "X11 display to capture")
		guiMode = flag.Bool("gui", false, "run the tray/settings GUI instead of the streaming daemon")
		show    = flag.Bool("show", false, "with -gui: open the settings window immediately")
	)
	flag.Parse()

	lg := log.New(os.Stderr, "anytable: ", log.LstdFlags)
	xgb.Logger = log.New(io.Discard, "", 0) // connection loss is expected (logout/boot); we handle it, not xgb

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *guiMode {
		err := gui.Run(ctx, gui.Options{
			AdminAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(AdminPort)),
			WebAddr:   net.JoinHostPort("127.0.0.1", strconv.Itoa(GUIPort)),
			Show:      *show,
			Log:       lg,
		})
		if err != nil {
			lg.Fatalf("gui: %v", err)
		}
		return
	}

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		lg.Fatalf("invalid configuration: %v", err)
	}
	lg.Printf("starting: config %s, display %s, port %d", *cfgPath, *display, cfg.Port)
	NewEngine(*cfgPath, *display, cfg, lg).Run(ctx)
	lg.Printf("stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
