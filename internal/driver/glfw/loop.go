package glfw

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal"
	"fyne.io/fyne/v2/internal/app"
	"fyne.io/fyne/v2/internal/cache"
	"fyne.io/fyne/v2/internal/painter"

	"github.com/go-gl/glfw/v3.3/glfw"
)

type funcData struct {
	f    func()
	done chan struct{} // Zero allocation signalling channel
}

type drawData struct {
	f    func()
	win  *window
	done chan struct{} // Zero allocation signalling channel
}

// channel for queuing functions on the main thread
var funcQueue = make(chan funcData)
var drawFuncQueue = make(chan drawData)
var runFlag uint32 = 0 // atomic, 0 == stopped, 1 == running
var initOnce = &sync.Once{}
var donePool = &sync.Pool{New: func() interface{} {
	return make(chan struct{})
}}

// Arrange that main.main runs on main thread.
func init() {
	runtime.LockOSThread()
}

func running() bool {
	return atomic.LoadUint32(&runFlag) == 1
}

// force a function f to run on the main thread
func runOnMain(f func()) {
	// If we are on main just execute - otherwise add it to the main queue and wait.
	// The "running" variable is normally false when we are on the main thread.
	if !running() {
		f()
	} else {
		done := donePool.Get().(chan struct{})
		defer donePool.Put(done)

		funcQueue <- funcData{f: f, done: done}
		<-done
	}
}

// force a function f to run on the draw thread
func runOnDraw(w *window, f func()) {
	done := donePool.Get().(chan struct{})
	defer donePool.Put(done)

	drawFuncQueue <- drawData{f: f, win: w, done: done}
	<-done
}

func (d *gLDriver) initGLFW() {
	initOnce.Do(func() {
		err := glfw.Init()
		if err != nil {
			fyne.LogError("failed to initialise GLFW", err)
			return
		}

		initCursors()
		d.startDrawThread()
	})
}

func (d *gLDriver) runGL() {
	eventTick := time.NewTicker(time.Second / 60)
	atomic.StoreUint32(&runFlag, 1)

	d.initGLFW()
	fyne.CurrentApp().Lifecycle().(*app.Lifecycle).TriggerStarted()
	for {
		select {
		case <-d.done:
			eventTick.Stop()
			d.drawDone <- nil // wait for draw thread to stop
			glfw.Terminate()
			fyne.CurrentApp().Lifecycle().(*app.Lifecycle).TriggerStopped()
			return
		case f := <-funcQueue:
			f.f()
			if f.done != nil {
				f.done <- struct{}{}
			}
		case <-eventTick.C:
			d.tryPollEvents()
			newWindows := []fyne.Window{}
			reassign := false
			for _, win := range d.windowList() {
				w := win.(*window)
				if w.viewport == nil {
					continue
				}

				if w.viewport.ShouldClose() {
					reassign = true
					w.viewLock.Lock()
					w.visible = false
					v := w.viewport
					w.viewLock.Unlock()

					// remove window from window list
					v.Destroy()
					w.destroy(d)
					continue
				}

				w.viewLock.RLock()
				expand := w.shouldExpand
				fullScreen := w.fullScreen
				w.viewLock.RUnlock()

				if expand && !fullScreen {
					w.fitContent()
					w.viewLock.Lock()
					shouldExpand := w.shouldExpand
					w.shouldExpand = false
					view := w.viewport
					w.viewLock.Unlock()
					if shouldExpand {
						view.SetSize(w.shouldWidth, w.shouldHeight)
					}
				}

				newWindows = append(newWindows, win)
			}
			if reassign {
				d.windowLock.Lock()
				d.windows = newWindows
				d.windowLock.Unlock()

				if len(newWindows) == 0 {
					d.Quit()
				}
			}
		}
	}
}

func (d *gLDriver) repaintWindow(w *window) {
	canvas := w.canvas
	w.RunWithContext(func() {
		if w.canvas.EnsureMinSize() {
			w.viewLock.Lock()
			w.shouldExpand = true
			w.viewLock.Unlock()
		}
		canvas.FreeDirtyTextures()

		updateGLContext(w)
		canvas.paint(canvas.Size())

		w.viewLock.RLock()
		view := w.viewport
		visible := w.visible
		w.viewLock.RUnlock()

		if view != nil && visible {
			view.SwapBuffers()
		}
	})
}

func (d *gLDriver) startDrawThread() {
	settingsChange := make(chan fyne.Settings)
	fyne.CurrentApp().Settings().AddChangeListener(settingsChange)
	draw := time.NewTicker(time.Second / 60)

	go func() {
		runtime.LockOSThread()

		for {
			select {
			case <-d.drawDone:
				return
			case f := <-drawFuncQueue:
				f.win.RunWithContext(f.f)
				if f.done != nil {
					f.done <- struct{}{}
				}
			case set := <-settingsChange:
				painter.ClearFontCache()
				cache.ResetThemeCaches()
				app.ApplySettingsWithCallback(set, fyne.CurrentApp(), func(w fyne.Window) {
					c, ok := w.Canvas().(*glCanvas)
					if !ok {
						return
					}
					c.applyThemeOutOfTreeObjects()
					go c.reloadScale()
				})
			case <-draw.C:
				refreshingCanvases := make([]fyne.Canvas, 0)
				for _, win := range d.windowList() {
					w := win.(*window)
					w.viewLock.RLock()
					canvas := w.canvas
					closing := w.closing
					visible := w.visible
					w.viewLock.RUnlock()

					// CheckDirtyAndClear must be checked after visibility,
					// because when a window becomes visible, it could be
					// showing old content without a dirty flag set to true.
					// Do the clear if and only if the window is visible.
					if closing || !visible || !canvas.CheckDirtyAndClear() {
						continue
					}

					d.repaintWindow(w)
					refreshingCanvases = append(refreshingCanvases, canvas)
				}
				cache.CleanCanvases(refreshingCanvases)
			}
		}
	}()
}

func (d *gLDriver) tryPollEvents() {
	defer func() {
		if r := recover(); r != nil {
			fyne.LogError(fmt.Sprint("GLFW poll event error: ", r), nil)
		}
	}()

	glfw.PollEvents() // This call blocks while window is being resized, which prevents freeDirtyTextures from being called
}

// refreshWindow requests that the specified window be redrawn
func refreshWindow(w *window) {
	w.canvas.SetDirty()
}

func updateGLContext(w *window) {
	canvas := w.Canvas().(*glCanvas)
	size := canvas.Size()

	// w.width and w.height are not correct if we are maximised, so figure from canvas
	winWidth := float32(internal.ScaleInt(canvas, size.Width)) * canvas.texScale
	winHeight := float32(internal.ScaleInt(canvas, size.Height)) * canvas.texScale

	canvas.Painter().SetFrameBufferScale(canvas.texScale)
	w.canvas.Painter().SetOutputSize(int(winWidth), int(winHeight))
}
