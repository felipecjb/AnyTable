package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"sync"

	"fyne.io/systray"
	"github.com/hajimehoshi/ebiten/v2"
)

type trayCmd uint8

const (
	trayCmdShow trayCmd = iota + 1
	trayCmdDisconnect
	trayCmdQuit
)

type trayController struct {
	cmds chan trayCmd
	once sync.Once
}

func startTray() *trayController {
	tc := &trayController{cmds: make(chan trayCmd, 8)}
	go func() {
		runtime.LockOSThread()
		systray.Run(func() {
			systray.SetIcon(appIconPNG())
			systray.SetTitle("AnyTable")
			systray.SetTooltip("AnyTable Viewer")
			systray.SetOnTapped(func() { tc.push(trayCmdShow) })

			showItem := systray.AddMenuItem("Show / Restore Window", "Restore the AnyTable viewer window")
			discItem := systray.AddMenuItem("Disconnect", "Close current remote session")
			systray.AddSeparator()
			quitItem := systray.AddMenuItem("Quit AnyTable", "Exit AnyTable viewer")

			go func() {
				for {
					select {
					case <-showItem.ClickedCh:
						tc.push(trayCmdShow)
					case <-discItem.ClickedCh:
						tc.push(trayCmdDisconnect)
					case <-quitItem.ClickedCh:
						tc.push(trayCmdQuit)
						return
					}
				}
			}()
		}, func() {})
	}()
	return tc
}

func (tc *trayController) push(cmd trayCmd) {
	select {
	case tc.cmds <- cmd:
	default:
	}
	ebiten.ScheduleFrame()
}

func (tc *trayController) stop() {
	tc.once.Do(func() {
		systray.Quit()
	})
}

func appIconImage() image.Image {
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
	return img
}

func appIconPNG() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, appIconImage())
	return b.Bytes()
}
