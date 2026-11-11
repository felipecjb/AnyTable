package hud

import (
	"context"
	"sync"
	"testing"
	"time"

	"anytable/common/protocol"
)

func TestPingTierThresholds(t *testing.T) {
	cases := []struct {
		ms   float64
		has  bool
		want PingTier
	}{
		{0, false, TierUnknown},
		{8.5, true, TierGood},
		{19.9, true, TierGood},
		{20.0, true, TierWarn},
		{45.0, true, TierWarn},
		{60.0, true, TierWarn},
		{60.1, true, TierBad},
		{140.0, true, TierBad},
	}
	for _, c := range cases {
		if got := PingTierFor(c.ms, c.has); got != c.want {
			t.Errorf("PingTierFor(%v, %v) = %v, want %v", c.ms, c.has, got, c.want)
		}
	}
}

func TestTelemetryPingPongAndBandwidth(t *testing.T) {
	tel := NewTelemetry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var sent [][]byte
	go tel.Run(ctx, func(raw []byte) error {
		mu.Lock()
		sent = append(sent, raw)
		mu.Unlock()
		p, err := protocol.DeserializePing(raw)
		if err == nil {
			// Simulate 5ms network RTT by backdating timestamp in returned PONG.
			pong := protocol.NewPongPacket(p.SequenceID, p.Timestamp-5000)
			tel.OnPong(pong)
		}
		return nil
	})

	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n >= 1 && tel.Snapshot().HasPing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("telemetry never sent PING or processed PONG (sent=%d)", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	st := tel.Snapshot()
	if st.PingMs < 4.0 || st.PingMs > 500.0 || st.Tier != TierGood {
		t.Fatalf("unexpected RTT snapshot: %+v", st)
	}
	if FormatBandwidth(512) != "512 B/s" || FormatBandwidth(2048) != "2 KB/s" || FormatBandwidth(2*1024*1024) != "2.0 MB/s" {
		t.Fatal("unexpected bandwidth formatting")
	}
}
