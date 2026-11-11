// Package hud tracks live connection telemetry (1 Hz RTT ping, stream FPS, bandwidth)
// and renders the floating overlay toolbar over the remote desktop canvas.
package hud

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"anytable/common/protocol"
)

// PingTier classifies network latency for the HUD status badge.
type PingTier uint8

const (
	TierUnknown PingTier = iota
	TierGood             // < 20ms (green)
	TierWarn             // 20-60ms (yellow)
	TierBad              // > 60ms (red)
)

// Stats is a thread-safe snapshot of current stream telemetry.
type Stats struct {
	PingMs      float64
	HasPing     bool
	Tier        PingTier
	FPS         float64
	BytesPerSec float64
}

// Telemetry owns the 1 Hz heartbeat loop and atomic counters for FPS/bandwidth.
type Telemetry struct {
	pingSeq uint64

	mu         sync.Mutex
	lastPingUs int64
	hasPing    bool

	winStart  int64 // UnixNano of current measurement window
	winFrames uint64
	winBytes  uint64
	fps       float64
	bps       float64
}

// NewTelemetry creates a ready telemetry tracker.
func NewTelemetry() *Telemetry {
	return &Telemetry{winStart: time.Now().UnixNano()}
}

// PingTierFor returns the color tier for a latency in milliseconds.
func PingTierFor(ms float64, hasSample bool) PingTier {
	if !hasSample {
		return TierUnknown
	}
	switch {
	case ms < 20.0:
		return TierGood
	case ms <= 60.0:
		return TierWarn
	default:
		return TierBad
	}
}

// Run transmits a sub-100-byte PING packet immediately and every 1000ms until ctx ends.
func (t *Telemetry) Run(ctx context.Context, sendPing func([]byte) error) {
	emit := func() error {
		pkt := protocol.NewPingPacket(atomic.AddUint64(&t.pingSeq, 1))
		raw, err := pkt.Serialize()
		if err != nil {
			return err
		}
		return sendPing(raw)
	}
	if err := emit(); err != nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := emit(); err != nil {
				return
			}
		}
	}
}

// OnPong computes RTT from the echoed client timestamp in a PONG packet.
func (t *Telemetry) OnPong(p *protocol.PingPacket) {
	if p == nil || p.Type != protocol.PacketTypePong {
		return
	}
	rtt := protocol.CalculateRTT(p.Timestamp)
	t.mu.Lock()
	t.lastPingUs = int64(rtt / time.Microsecond)
	t.hasPing = true
	t.mu.Unlock()
}

// RecordPacket updates frame and bandwidth counters from incoming stream traffic.
func (t *Telemetry) RecordPacket(packetBytes int, isFrame bool) {
	now := time.Now().UnixNano()
	t.mu.Lock()
	if packetBytes > 0 {
		t.winBytes += uint64(packetBytes)
	}
	if isFrame {
		t.winFrames++
	}
	elapsed := now - t.winStart
	if elapsed >= int64(time.Second) {
		sec := float64(elapsed) / float64(time.Second)
		t.fps = float64(t.winFrames) / sec
		t.bps = float64(t.winBytes) / sec
		t.winFrames = 0
		t.winBytes = 0
		t.winStart = now
	}
	t.mu.Unlock()
}

// Snapshot returns the current latency, FPS, and bandwidth values.
func (t *Telemetry) Snapshot() Stats {
	now := time.Now().UnixNano()
	t.mu.Lock()
	defer t.mu.Unlock()

	fps, bps := t.fps, t.bps
	if elapsed := now - t.winStart; elapsed >= 2*int64(time.Second) {
		// Stream went idle (no frames for 2s); decay display to 0.
		sec := float64(elapsed) / float64(time.Second)
		fps = float64(t.winFrames) / sec
		bps = float64(t.winBytes) / sec
	}
	ms := float64(t.lastPingUs) / 1000.0
	return Stats{
		PingMs:      ms,
		HasPing:     t.hasPing,
		Tier:        PingTierFor(ms, t.hasPing),
		FPS:         fps,
		BytesPerSec: bps,
	}
}

// FormatBandwidth formats a byte/sec throughput rate concisely for the HUD.
func FormatBandwidth(bps float64) string {
	switch {
	case bps >= 1024*1024:
		return fmt.Sprintf("%.1f MB/s", bps/(1024*1024))
	case bps >= 1024:
		return fmt.Sprintf("%.0f KB/s", bps/1024)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}
