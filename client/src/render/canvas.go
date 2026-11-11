package render

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"

	"anytable/client/src/input"
)

// FitRect calculates the destination rectangle (x, y, w, h) inside canvasW x canvasH
// for a remote frame of frameW x frameH, preserving aspect ratio unless stretch is true.
func FitRect(canvasW, canvasH, frameW, frameH int, stretch bool) input.CanvasRect {
	if canvasW <= 0 || canvasH <= 0 || frameW <= 0 || frameH <= 0 {
		return input.CanvasRect{}
	}
	if stretch {
		return input.CanvasRect{X: 0, Y: 0, W: float64(canvasW), H: float64(canvasH)}
	}
	cw, ch := float64(canvasW), float64(canvasH)
	fw, fh := float64(frameW), float64(frameH)

	scale := cw / fw
	if sh := ch / fh; sh < scale {
		scale = sh
	}
	dw := fw * scale
	dh := fh * scale
	dx := (cw - dw) / 2
	dy := (ch - dh) / 2
	return input.CanvasRect{X: dx, Y: dy, W: dw, H: dh}
}

// Canvas maintains the hardware-backed Ebitengine texture for the remote desktop stream.
type Canvas struct {
	tex     *ebiten.Image
	texW    int
	texH    int
	lastSeq uint64
}

// NewCanvas allocates a Canvas renderer.
func NewCanvas() *Canvas {
	return &Canvas{}
}

// Draw uploads any newly decoded frame and renders it onto dst, returning the video rect.
func (c *Canvas) Draw(dst *ebiten.Image, frame *DecodedFrame, fallbackW, fallbackH int, stretch bool) input.CanvasRect {
	dst.Fill(color.Black)
	cw, ch := dst.Bounds().Dx(), dst.Bounds().Dy()

	if frame == nil || frame.W <= 0 || frame.H <= 0 || len(frame.Pix) < frame.W*frame.H*4 {
		return FitRect(cw, ch, fallbackW, fallbackH, stretch)
	}

	if c.tex == nil || c.texW != frame.W || c.texH != frame.H {
		if c.tex != nil {
			c.tex.Deallocate()
		}
		c.tex = ebiten.NewImage(frame.W, frame.H)
		c.texW, c.texH = frame.W, frame.H
		c.lastSeq = 0
	}

	if frame.Seq != c.lastSeq {
		c.tex.WritePixels(frame.Pix)
		c.lastSeq = frame.Seq
	}

	rect := FitRect(cw, ch, frame.W, frame.H, stretch)
	op := &ebiten.DrawImageOptions{}
	op.Filter = ebiten.FilterLinear
	op.GeoM.Scale(rect.W/float64(frame.W), rect.H/float64(frame.H))
	op.GeoM.Translate(rect.X, rect.Y)
	dst.DrawImage(c.tex, op)
	return rect
}

// Reset frees the GPU texture on disconnect.
func (c *Canvas) Reset() {
	if c.tex != nil {
		c.tex.Deallocate()
		c.tex = nil
	}
	c.texW, c.texH, c.lastSeq = 0, 0, 0
}
