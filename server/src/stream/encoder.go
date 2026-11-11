package stream

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"time"

	"anytable/server/src/capture"
)

const minAdaptiveQuality = 30

// Encoder turns raw frames into JPEG with optional nearest-neighbour downscale and
// adaptive quality. Pure Go (stdlib image/jpeg) so the daemon stays CGO-free/static;
// ponytail: swap in a libjpeg-turbo binding if ARM encode speed becomes the bottleneck.
// Not safe for concurrent use; one Encoder per client.
type Encoder struct {
	base int
	q    int
	buf  bytes.Buffer
	img  *image.RGBA
	xs   []int
}

// NewEncoder creates an encoder with the configured (maximum) quality, 1-100.
func NewEncoder(quality int) *Encoder {
	e := &Encoder{}
	e.SetBaseQuality(quality)
	e.q = e.base
	return e
}

// SetBaseQuality changes the configured quality ceiling.
func (e *Encoder) SetBaseQuality(q int) {
	q = clamp(q, 1, 100)
	if q != e.base {
		e.base = q
		e.q = clamp(e.q, e.floor(), q)
	}
}

// Quality returns the quality used for the next frame.
func (e *Encoder) Quality() int { return e.q }

func (e *Encoder) floor() int {
	lo := e.base - 40
	if lo < minAdaptiveQuality {
		lo = minAdaptiveQuality
	}
	if lo > e.base {
		lo = e.base
	}
	return lo
}

// Adapt lowers quality when encode+send took longer than the frame budget and
// recovers it slowly when there is headroom.
func (e *Encoder) Adapt(elapsed, budget time.Duration) {
	switch {
	case elapsed > budget:
		e.q = clamp(e.q-5, e.floor(), e.base)
	case elapsed < budget/2:
		e.q = clamp(e.q+2, e.floor(), e.base)
	}
}

// FitSize returns the size of a w x h frame scaled down (aspect preserved) to fit maxW x maxH.
// maxW/maxH <= 0 or larger than the source mean no scaling.
func FitSize(w, h, maxW, maxH int) (int, int) {
	if maxW <= 0 || maxH <= 0 || (w <= maxW && h <= maxH) {
		return w, h
	}
	sw, sh := float64(maxW)/float64(w), float64(maxH)/float64(h)
	s := sw
	if sh < s {
		s = sh
	}
	nw, nh := int(float64(w)*s), int(float64(h)*s)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return nw, nh
}

// Encode returns the JPEG of f scaled to fit maxW x maxH. The slice is only valid until the next Encode call.
func (e *Encoder) Encode(f *capture.Frame, maxW, maxH int) ([]byte, error) {
	if f == nil || f.W <= 0 || f.H <= 0 || len(f.Pix) < f.W*f.H*4 {
		return nil, errors.New("stream: invalid frame")
	}
	dw, dh := FitSize(f.W, f.H, maxW, maxH)
	if e.img == nil || e.img.Rect.Dx() != dw || e.img.Rect.Dy() != dh {
		e.img = image.NewRGBA(image.Rect(0, 0, dw, dh))
	}
	e.convert(f, dw, dh)
	e.buf.Reset()
	if err := jpeg.Encode(&e.buf, e.img, &jpeg.Options{Quality: e.q}); err != nil {
		return nil, err
	}
	return e.buf.Bytes(), nil
}

// convert copies the X pixels into e.img as RGBA, scaling by nearest neighbour when dw,dh differ from the source.
func (e *Encoder) convert(f *capture.Frame, dw, dh int) {
	dst := e.img.Pix
	src := f.Pix
	if dw == f.W && dh == f.H {
		n := dw * dh * 4
		if f.BGR {
			for i := 0; i < n; i += 4 {
				dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], 255
			}
		} else {
			for i := 0; i < n; i += 4 {
				dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i], src[i+1], src[i+2], 255
			}
		}
		return
	}
	if cap(e.xs) < dw {
		e.xs = make([]int, dw)
	}
	xs := e.xs[:dw]
	for dx := range xs {
		xs[dx] = dx * f.W / dw * 4
	}
	for dy := 0; dy < dh; dy++ {
		row := src[(dy*f.H/dh)*f.W*4:]
		out := dst[dy*dw*4:]
		for dx, sx := range xs {
			o := dx * 4
			if f.BGR {
				out[o], out[o+1], out[o+2] = row[sx+2], row[sx+1], row[sx]
			} else {
				out[o], out[o+1], out[o+2] = row[sx], row[sx+1], row[sx+2]
			}
			out[o+3] = 255
		}
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
