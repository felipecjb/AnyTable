// Package input captures local mouse/keyboard events over the remote canvas,
// maps coordinates to normalized [0.0, 1.0] space, and emits protocol packets.
package input

// CanvasRect is the rendered destination rectangle of the remote display in local window pixels.
type CanvasRect struct {
	X, Y, W, H float64
}

// NormalizeMouse maps window pointer coordinates (mx, my) relative to rect into [0.0, 1.0].
// inside reports whether the pointer was inside rect before clamping.
func NormalizeMouse(mx, my float64, rect CanvasRect) (x, y float32, inside, valid bool) {
	if rect.W <= 0 || rect.H <= 0 {
		return 0, 0, false, false
	}
	nx := (mx - rect.X) / rect.W
	ny := (my - rect.Y) / rect.H
	inside = nx >= 0.0 && nx <= 1.0 && ny >= 0.0 && ny <= 1.0
	return clamp01(nx), clamp01(ny), inside, true
}

func clamp01(v float64) float32 {
	if v < 0.0 {
		return 0.0
	}
	if v > 1.0 {
		return 1.0
	}
	return float32(v)
}
