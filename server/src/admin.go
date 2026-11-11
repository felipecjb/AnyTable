package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Loopback-only ports of the local settings interface (never exposed to the LAN).
const (
	AdminPort = 18443 // engine admin API (daemon)
	GUIPort   = 18444 // settings web UI served by the GUI process
)

// serveAdmin runs the admin API on 127.0.0.1 until ctx ends. If the port is unavailable the
// daemon keeps streaming without it.
func (e *Engine) serveAdmin(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, e.Status())
	})
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var u SettingsUpdate
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&u); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := e.UpdateSettings(u); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, e.Status())
	})

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(AdminPort)))
	if err != nil {
		e.log.Printf("admin API disabled: %v", err)
		<-ctx.Done()
		return
	}
	srv := &http.Server{Handler: guardLocal(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	_ = srv.Serve(ln)
}

// guardLocal blocks DNS-rebinding (Host must be loopback) and cross-site form posts
// (mutations must be application/json, which browsers cannot send cross-origin without CORS preflight).
func guardLocal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
