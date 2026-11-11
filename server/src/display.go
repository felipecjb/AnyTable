package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jezek/xgb"
)

const displayPollInterval = 2 * time.Second

// DisplayHandler is notified when the X display becomes usable or goes away.
// Both calls run on the display goroutine; DisplayUp must return quickly.
// If DisplayUp fails the connection is dropped and retried after the poll interval.
type DisplayHandler interface {
	DisplayUp(conn *xgb.Conn) error
	DisplayDown()
}

// DisplayManager keeps a connection to the local X display alive for the lifetime of the daemon.
// X not ready yet (boot) or user logged out: it polls silently every 2s and reconnects at once when X returns.
type DisplayManager struct {
	display string
	root    string // filesystem root for authority discovery ("/" in production)
	handler DisplayHandler
	log     *log.Logger

	envMu sync.Mutex // xgb reads $XAUTHORITY at dial time; serialize our os.Setenv

	stMu sync.RWMutex
	up   bool
	auth string
}

// NewDisplayManager creates a manager for the given display (e.g. ":0").
func NewDisplayManager(display string, h DisplayHandler, lg *log.Logger) *DisplayManager {
	return &DisplayManager{display: display, root: string(filepath.Separator), handler: h, log: lg}
}

// State reports whether the display is connected and which Xauthority file worked.
func (m *DisplayManager) State() (up bool, authority string) {
	m.stMu.RLock()
	defer m.stMu.RUnlock()
	return m.up, m.auth
}

func (m *DisplayManager) setState(up bool, auth string) {
	m.stMu.Lock()
	m.up, m.auth = up, auth
	m.stMu.Unlock()
}

// Run blocks until ctx ends. It never returns early and never panics out on X errors.
func (m *DisplayManager) Run(ctx context.Context) {
	waiting := false
	lastAuth := ""
	for ctx.Err() == nil {
		conn, auth, err := m.connect(lastAuth)
		if err == nil {
			if herr := m.handler.DisplayUp(conn); herr != nil {
				m.log.Printf("display %s connected but unusable: %v", m.display, herr)
				conn.Close()
				err = herr
			}
		}
		if err != nil {
			if !waiting {
				m.log.Printf("waiting for X display %s: %v", m.display, err)
				waiting = true
			}
			if !sleepCtx(ctx, displayPollInterval) {
				return
			}
			continue
		}
		waiting, lastAuth = false, auth
		m.log.Printf("connected to X display %s (authority %q)", m.display, auth)
		m.setState(true, auth)

		dead := make(chan struct{})
		go func() { // drains X events (required, or xgb stalls) and detects connection loss
			defer close(dead)
			for {
				if ev, xerr := conn.WaitForEvent(); ev == nil && xerr == nil {
					return
				}
			}
		}()
		select {
		case <-dead:
			m.log.Printf("X display %s went away; polling every %s", m.display, displayPollInterval)
		case <-ctx.Done():
		}
		m.setState(false, "")
		m.handler.DisplayDown()
		conn.Close()
		select {
		case <-dead:
		case <-time.After(time.Second):
		}
	}
}

// connect tries each candidate Xauthority (last working one first), then the xgb default.
func (m *DisplayManager) connect(lastAuth string) (*xgb.Conn, string, error) {
	cands := findAuthorityFiles(m.root, os.Getenv("XAUTHORITY"))
	if lastAuth != "" {
		cands = append([]string{lastAuth}, removeString(cands, lastAuth)...)
	}
	cands = append(cands, "") // "" = leave $XAUTHORITY alone (xgb falls back to $HOME/.Xauthority)

	m.envMu.Lock()
	defer m.envMu.Unlock()
	orig, had := os.LookupEnv("XAUTHORITY")
	defer func() {
		if had {
			os.Setenv("XAUTHORITY", orig)
		} else {
			os.Unsetenv("XAUTHORITY")
		}
	}()
	var lastErr error
	for _, path := range cands {
		if path != "" {
			os.Setenv("XAUTHORITY", path)
		} else if had {
			os.Setenv("XAUTHORITY", orig)
		} else {
			os.Unsetenv("XAUTHORITY")
		}
		conn, err := dialX(m.display)
		if err == nil {
			return conn, path, nil
		}
		lastErr = err
	}
	return nil, "", lastErr
}

func dialX(display string) (conn *xgb.Conn, err error) {
	defer func() {
		if r := recover(); r != nil {
			conn, err = nil, errors.New("X11 dial panicked")
		}
	}()
	return xgb.NewConnDisplay(display)
}

// findAuthorityFiles lists readable Xauthority files below root, newest first (the active
// session's file is the most recently written). $XAUTHORITY, when set, always comes first.
func findAuthorityFiles(root, envPath string) []string {
	patterns := []string{
		"home/*/.Xauthority",
		"run/user/*/gdm/Xauthority",
		"run/user/*/.Xauthority",
		"run/gdm3/auth-for-*/database",
		"var/run/lightdm/root/:*",
		"run/lightdm/root/:*",
		"var/run/sddm/*",
		"root/.Xauthority",
	}
	type cand struct {
		path string
		mod  time.Time
	}
	var found []cand
	seen := map[string]bool{}
	for _, p := range patterns {
		matches, _ := filepath.Glob(filepath.Join(root, p))
		for _, path := range matches {
			if seen[path] {
				continue
			}
			if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
				seen[path] = true
				found = append(found, cand{path, st.ModTime()})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].mod.After(found[j].mod) })
	var out []string
	if envPath != "" {
		out = append(out, envPath)
		seen[envPath] = true
	}
	for _, c := range found {
		if c.path != envPath {
			out = append(out, c.path)
		}
	}
	return out
}

func removeString(list []string, s string) []string {
	out := list[:0:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
