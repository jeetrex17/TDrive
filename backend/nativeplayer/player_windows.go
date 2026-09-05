//go:build windows

package nativeplayer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsClipChildren = 0x02000000
	wsClipSiblings = 0x04000000

	hwndTop    = 0
	hwndBottom = 1

	pmRemove = 0x0001

	swpNoActivate = 0x0010
	swpShowWindow = 0x0040

	swHide = 0

	jobObjectInfoClassExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose               = 0x00002000
)

const windowsNativePlayerFlag = "TDRIVE_EXPERIMENTAL_WINDOWS_NATIVE_PLAYER"

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")

	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")

	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procEnumWindows            = user32.NewProc("EnumWindows")
	procGetDpiForWindow        = user32.NewProc("GetDpiForWindow")
	procGetWindowTextLengthW   = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcess = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
	procPeekMessageW           = user32.NewProc("PeekMessageW")
	procSetWindowPos           = user32.NewProc("SetWindowPos")
	procShowWindow             = user32.NewProc("ShowWindow")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")

	enumWindowsMu       sync.Mutex
	enumWindowsCallback = syscall.NewCallback(enumWindowsProc)
	enumTargetPID       uint32
	enumFirstWindow     uintptr
	enumTitledWindow    uintptr

	errWindowThreadStop = fmt.Errorf("native player: stop window thread")
)

type Player struct {
	mu            sync.Mutex
	closed        bool
	terminal      bool
	parent        uintptr
	child         uintptr
	windowOwner   *windowsWindowThread
	ipcPath       string
	proc          *windowsMPVProcess
	done          chan error
	job           syscall.Handle
	stateCancel   context.CancelFunc
	eventConn     *os.File
	eventDone     chan struct{}
	onState       StateHandler
	lastState     State
	observedState *mpvObservedProperties

	destroyOnce sync.Once
	jobOnce     sync.Once
}

func Start(ctx context.Context, url string, rect Rect, opts Options) (*Player, error) {
	if !windowsNativePlayerEnabled() {
		return nil, ErrUnsupported
	}
	if !rect.Valid() {
		return nil, fmt.Errorf("native player: invalid view rect")
	}

	parent, err := findTDriveWindow()
	if err != nil {
		return nil, err
	}
	owner := startWindowsWindowThread()
	child, err := owner.create(parent, rect, opts.UseHTMLControls)
	if err != nil {
		owner.close()
		return nil, err
	}

	player := &Player{parent: parent, child: child, windowOwner: owner, done: make(chan error, 1)}
	if err := player.startProcess(ctx, url, opts); err != nil {
		player.destroyChild()
		owner.close()
		return nil, err
	}
	return player, nil
}

func (p *Player) startProcess(ctx context.Context, url string, opts Options) error {
	mpvPath, err := findWindowsMPV()
	if err != nil {
		return err
	}
	job, err := createKillOnCloseJob()
	if err != nil {
		return err
	}
	p.job = job

	suffix, err := randomPipeSuffix()
	if err != nil {
		p.closeJob()
		return err
	}
	p.ipcPath = fmt.Sprintf(`\\.\pipe\tdrive-mpv-%d-%s`, os.Getpid(), suffix)
	args := []string{
		"--no-config",
		"--terminal=no",
		"--msg-level=all=warn",
		"--ytdl=no",
		"--hwdec=auto-safe",
		"--cache=yes",
		"--demuxer-readahead-secs=4",
		"--demuxer-max-bytes=8388608",
		"--demuxer-max-back-bytes=4194304",
		"--keepaspect=yes",
		"--keepaspect-window=no",
		"--auto-window-resize=no",
		"--video-align-x=0",
		"--video-align-y=0",
		"--osc=no",
		"--osd-bar=no",
		"--osd-level=0",
		"--cursor-autohide=no",
		"--no-input-default-bindings",
		"--force-window=immediate",
		"--input-vo-keyboard=no",
		"--input-terminal=no",
		"--idle=yes",
		"--keep-open=yes",
		"--input-ipc-server=" + p.ipcPath,
		fmt.Sprintf("--wid=%d", p.child),
	}

	proc, err := startSuspendedMPV(ctx, mpvPath, args)
	if err != nil {
		p.closeJob()
		return err
	}
	if err := assignProcessToJob(job, proc.process); err != nil {
		proc.kill()
		_ = proc.wait()
		p.closeJob()
		return err
	}
	if err := proc.resume(); err != nil {
		proc.kill()
		_ = proc.wait()
		p.closeJob()
		return err
	}
	p.proc = proc
	p.onState = opts.OnState
	if opts.OnState != nil {
		p.emitState(normalizeState(State{Status: StatusOpening, Paused: true, Loading: true, Volume: 1, Rate: 1}))
	}
	eventConn, err := openMPVIPCReadWriteWithAttempts(p.ipcPath, 80)
	if err != nil {
		proc.kill()
		_ = proc.wait()
		p.proc = nil
		p.closeJob()
		return err
	}
	stateCtx, cancel := context.WithCancel(ctx)
	p.stateCancel = cancel
	p.eventConn = eventConn
	p.eventDone = make(chan struct{})
	if opts.OnState != nil {
		_ = eventConn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
		if err := writeMPVObserveProperties(eventConn, mpvStatePropertyNames); err != nil {
			cancel()
			_ = eventConn.Close()
			proc.kill()
			_ = proc.wait()
			p.proc = nil
			p.closeJob()
			return fmt.Errorf("native player: observe mpv state: %w", err)
		}
		_ = eventConn.SetWriteDeadline(time.Time{})
	}
	go p.observeEvents(stateCtx, eventConn, p.eventDone)
	if err := writeMPVIPCWithAttempts(p.ipcPath, mpvCommandPayload("loadfile", url, "replace"), 80); err != nil {
		cancel()
		_ = eventConn.Close()
		proc.kill()
		_ = proc.wait()
		p.proc = nil
		p.closeJob()
		return fmt.Errorf("native player: initialize mpv IPC: %w", err)
	}
	go func() {
		err := proc.wait()
		p.handleProcessExit(err)
		p.done <- err
		p.destroyChild()
		p.closeJob()
	}()
	return nil
}

func (p *Player) Presentation() Presentation {
	return PresentationEmbedded
}

func randomPipeSuffix() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("native player: generate IPC name: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (p *Player) Resize(rect Rect) error {
	if p == nil {
		return nil
	}
	if !rect.Valid() {
		return fmt.Errorf("native player: invalid view rect")
	}

	p.mu.Lock()
	owner := p.windowOwner
	if p.closed || p.child == 0 {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	if owner == nil {
		return nil
	}
	return owner.resize(rect)
}

// ShowSeekThumbnail decodes data (a JPEG/PNG seek thumbnail), scales it to rect
// (CSS pixels, converted to physical pixels via the window DPI) and paints it on
// an overlay window above the video. WebView2 cannot draw HTML over the native
// mpv surface, so this gives the Windows fallback an on-video scrub preview.
func (p *Player) ShowSeekThumbnail(data []byte, rect Rect) error {
	if p == nil || !rect.Valid() {
		return nil
	}
	p.mu.Lock()
	owner := p.windowOwner
	parent := p.parent
	closed := p.closed
	p.mu.Unlock()
	overlayDebugf("ShowSeekThumbnail bytes=%d rect=%+v closed=%v parent=%d hasOwner=%v", len(data), rect, closed, parent, owner != nil)
	if closed || owner == nil || parent == 0 {
		return nil
	}
	x, y, w, h := rectToWindowCoords(parent, rect)
	bmp, err := composeSeekThumbnail(data, w, h)
	if err != nil {
		overlayDebugf("composeSeekThumbnail err=%v", err)
		return err
	}
	overlayDebugf("composed %dx%d -> show at x=%d y=%d w=%d h=%d", bmp.Width, bmp.Height, x, y, w, h)
	return owner.showThumbnail(bmp, x, y, w, h)
}

// MoveSeekThumbnail repositions the existing seek-preview overlay without
// decoding or uploading a new bitmap. This is the hot path while the cursor
// moves inside the same thumbnail bucket.
func (p *Player) MoveSeekThumbnail(rect Rect) error {
	if p == nil || !rect.Valid() {
		return nil
	}
	p.mu.Lock()
	owner := p.windowOwner
	parent := p.parent
	closed := p.closed
	p.mu.Unlock()
	if closed || owner == nil || parent == 0 {
		return nil
	}
	x, y, w, h := rectToWindowCoords(parent, rect)
	return owner.moveThumbnail(x, y, w, h)
}

// HideSeekThumbnail hides the seek-thumbnail overlay, leaving it ready to reuse.
func (p *Player) HideSeekThumbnail() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	owner := p.windowOwner
	p.mu.Unlock()
	if owner == nil {
		return nil
	}
	return owner.hideThumbnail()
}

func (p *Player) Command(command ...string) error {
	if p == nil || len(command) == 0 {
		return nil
	}

	p.mu.Lock()
	ipcPath := p.ipcPath
	closed := p.closed
	p.mu.Unlock()
	if closed || ipcPath == "" {
		return nil
	}

	return writeMPVIPC(ipcPath, mpvCommandPayload(command...))
}

func (p *Player) Close() error {
	if p == nil {
		return nil
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	ipcPath := p.ipcPath
	proc := p.proc
	done := p.done
	stateCancel := p.stateCancel
	eventConn := p.eventConn
	eventDone := p.eventDone
	p.mu.Unlock()

	if ipcPath != "" {
		_ = writeMPVIPC(ipcPath, []byte(`{"command":["quit"]}`+"\n"))
	}
	if stateCancel != nil {
		stateCancel()
	}
	if eventConn != nil {
		_ = eventConn.Close()
	}
	if proc != nil {
		select {
		case <-done:
		case <-time.After(750 * time.Millisecond):
			proc.kill()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
	}
	if eventDone != nil {
		select {
		case <-eventDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	p.destroyChild()
	p.closeJob()
	p.emitTerminal(StatusClosed)
	return nil
}

func (p *Player) destroyChild() {
	if p == nil {
		return
	}
	p.destroyOnce.Do(func() {
		p.mu.Lock()
		child := p.child
		p.child = 0
		owner := p.windowOwner
		p.windowOwner = nil
		p.mu.Unlock()
		if owner != nil {
			_ = owner.destroy()
			owner.close()
			return
		}
		if child != 0 {
			hideAndDestroyWindow(child)
		}
	})
}

func (p *Player) closeJob() {
	if p == nil {
		return
	}
	p.jobOnce.Do(func() {
		p.mu.Lock()
		job := p.job
		p.job = 0
		p.mu.Unlock()
		if job != 0 {
			_ = syscall.CloseHandle(job)
		}
	})
}

func writeMPVIPC(path string, payload []byte) error {
	return writeMPVIPCWithAttempts(path, payload, 20)
}

func writeMPVIPCWithAttempts(path string, payload []byte, attempts int) error {
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err == nil {
			_ = file.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
			return writeAndCloseWithTimeout(file, payload, 600*time.Millisecond)
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("native player: mpv IPC unavailable: %w", lastErr)
}

func (p *Player) emitState(state State) {
	state = normalizeState(state)
	p.mu.Lock()
	if p.closed || p.terminal {
		p.mu.Unlock()
		return
	}
	p.lastState = state
	onState := p.onState
	p.mu.Unlock()
	if onState != nil {
		onState(state)
	}
}

func (p *Player) emitTerminal(status PlaybackStatus) {
	state := terminalState(status)
	p.mu.Lock()
	if p.terminal {
		p.mu.Unlock()
		return
	}
	p.terminal = true
	p.lastState = state
	onState := p.onState
	p.mu.Unlock()
	if onState != nil {
		onState(state)
	}
}

func (p *Player) handleProcessExit(err error) {
	p.mu.Lock()
	closed := p.closed
	stateCancel := p.stateCancel
	eventConn := p.eventConn
	p.mu.Unlock()
	if stateCancel != nil {
		stateCancel()
	}
	if eventConn != nil {
		_ = eventConn.Close()
	}
	switch sidecarExitStatus(closed, false, err) {
	case StatusClosed:
		p.emitTerminal(StatusClosed)
	default:
		p.emitTerminal(StatusFailed)
	}
}

func (p *Player) observeEvents(ctx context.Context, file *os.File, done chan<- struct{}) {
	defer close(done)
	defer file.Close()
	stopClose := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = file.Close()
		case <-stopClose:
		}
	}()
	defer close(stopClose)
	_ = scanMPVEvents(file, func(event mpvIPCEvent) {
		p.handleMPVEvent(event)
	})
}

func (p *Player) handleMPVEvent(event mpvIPCEvent) {
	if event.Event == "property-change" {
		p.updateObservedState(event)
		return
	}
	status, ok := mpvEventStatus(event)
	if !ok {
		return
	}
	if status == StatusEnded {
		p.mu.Lock()
		lastState := p.lastState
		p.mu.Unlock()
		p.emitState(endedState(lastState))
		return
	}
	p.emitTerminal(status)
}

func (p *Player) updateObservedState(event mpvIPCEvent) {
	if event.Name == "" {
		return
	}
	p.mu.Lock()
	if p.closed || p.terminal {
		p.mu.Unlock()
		return
	}
	if p.observedState == nil {
		p.observedState = newMPVObservedProperties(mpvStatePropertyNames)
	}
	values, ready := p.observedState.update(event, time.Now())
	p.mu.Unlock()
	if !ready {
		return
	}
	p.emitState(stateFromMPVProperties(values))
}

func openMPVIPCReadWrite(path string) (*os.File, error) {
	return openMPVIPCReadWriteWithAttempts(path, 4)
}

func openMPVIPCReadWriteWithAttempts(path string, attempts int) (*os.File, error) {
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err == nil {
			return file, nil
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	return nil, fmt.Errorf("native player: mpv IPC unavailable: %w", lastErr)
}

func findTDriveWindow() (uintptr, error) {
	enumWindowsMu.Lock()
	defer enumWindowsMu.Unlock()

	enumTargetPID = uint32(os.Getpid())
	enumFirstWindow = 0
	enumTitledWindow = 0
	ret, _, callErr := procEnumWindows.Call(enumWindowsCallback, 0)
	first := enumFirstWindow
	titled := enumTitledWindow
	enumTargetPID = 0
	enumFirstWindow = 0
	enumTitledWindow = 0

	if ret == 0 && titled == 0 && first == 0 {
		return 0, callFailed("EnumWindows", callErr)
	}
	if titled != 0 {
		return titled, nil
	}
	if first != 0 {
		return first, nil
	}
	return 0, fmt.Errorf("native player: TDrive window not found")
}

func enumWindowsProc(hwnd uintptr, lparam uintptr) uintptr {
	if !windowVisible(hwnd) || windowProcessID(hwnd) != enumTargetPID {
		return 1
	}
	if enumFirstWindow == 0 {
		enumFirstWindow = hwnd
	}
	if strings.Contains(windowText(hwnd), "TDrive") {
		enumTitledWindow = hwnd
		return 0
	}
	return 1
}

func createVideoChildWindow(parent uintptr, rect Rect, htmlControls bool) (uintptr, error) {
	x, y, w, h := rectToWindowCoords(parent, rect)
	className, _ := syscall.UTF16PtrFromString("STATIC")
	title, _ := syscall.UTF16PtrFromString("TDriveNativeVideo")
	style := uintptr(wsChild | wsVisible | wsClipChildren | wsClipSiblings)
	child, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		style,
		uintptr(x),
		uintptr(y),
		uintptr(w),
		uintptr(h),
		parent,
		0,
		0,
		0,
	)
	if child == 0 {
		return 0, callFailed("CreateWindowExW", callErr)
	}
	if err := positionVideoChildWindow(child, x, y, w, h, htmlControls); err != nil {
		procDestroyWindow.Call(child)
		return 0, err
	}
	return child, nil
}

func hideAndDestroyWindow(child uintptr) {
	procShowWindow.Call(child, swHide)
	procDestroyWindow.Call(child)
}

func positionVideoChildWindow(child uintptr, x, y, w, h int, htmlControls bool) error {
	insertAfter := uintptr(hwndTop)
	if htmlControls {
		// Experimental overlay mode: Wails makes WebView2 transparent-capable,
		// and CSS punches a transparent video stage. Keep mpv under WebView2 so
		// HTML controls and popovers can render above it. The fallback path keeps
		// mpv on top because some WebView2/windowed compositions otherwise hide
		// the child HWND behind an opaque webview repaint.
		insertAfter = hwndBottom
	}
	ret, _, callErr := procSetWindowPos.Call(
		child,
		insertAfter,
		uintptr(x),
		uintptr(y),
		uintptr(w),
		uintptr(h),
		swpNoActivate|swpShowWindow,
	)
	if ret == 0 {
		return callFailed("SetWindowPos", callErr)
	}
	return nil
}

type windowsWindowThread struct {
	requests chan windowsWindowRequest
	done     chan struct{}
	// parent, child, and the overlay handles are created, read, and destroyed
	// only on the window thread itself (inside call closures), so they need no
	// further locking. overlay is the STATIC window that paints seek thumbnails
	// above the video; overlayBmp is the HBITMAP it currently displays.
	parent       uintptr
	child        uintptr
	htmlControls bool
	overlay      uintptr
	overlayBmp   uintptr
}

type windowsWindowRequest struct {
	fn    func() error
	reply chan error
}

type winPoint struct {
	X int32
	Y int32
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      winPoint
}

func startWindowsWindowThread() *windowsWindowThread {
	t := &windowsWindowThread{
		requests: make(chan windowsWindowRequest),
		done:     make(chan struct{}),
	}
	go t.run()
	return t
}

func (t *windowsWindowThread) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)

	ticker := time.NewTicker(8 * time.Millisecond)
	defer ticker.Stop()

	for {
		pumpWindowMessages()
		select {
		case req, ok := <-t.requests:
			if !ok {
				pumpWindowMessages()
				return
			}
			err := req.fn()
			if err == errWindowThreadStop {
				req.reply <- nil
				pumpWindowMessages()
				return
			}
			req.reply <- err
		case <-ticker.C:
		}
	}
}

func (t *windowsWindowThread) call(ctx context.Context, fn func() error) error {
	if t == nil {
		return fmt.Errorf("native player: window thread is closed")
	}
	reply := make(chan error, 1)
	req := windowsWindowRequest{fn: fn, reply: reply}
	select {
	case t.requests <- req:
	case <-t.done:
		return fmt.Errorf("native player: window thread is closed")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-reply:
		return err
	case <-t.done:
		return fmt.Errorf("native player: window thread is closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *windowsWindowThread) create(parent uintptr, rect Rect, htmlControls bool) (uintptr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var child uintptr
	err := t.call(ctx, func() error {
		t.parent = parent
		t.htmlControls = htmlControls
		var createErr error
		child, createErr = createVideoChildWindow(parent, rect, htmlControls)
		if createErr == nil {
			t.child = child
		}
		return createErr
	})
	return child, err
}

func (t *windowsWindowThread) resize(rect Rect) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return t.call(ctx, func() error {
		if t.child == 0 {
			return nil
		}
		x, y, w, h := rectToWindowCoords(t.parent, rect)
		return positionVideoChildWindow(t.child, x, y, w, h, t.htmlControls)
	})
}

func (t *windowsWindowThread) destroy() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return t.call(ctx, func() error {
		t.destroyOverlay()
		if t.child != 0 {
			hideAndDestroyWindow(t.child)
			t.child = 0
		}
		return nil
	})
}

// destroyOverlay tears down the seek-thumbnail overlay window and its bitmap.
// Must run on the window thread.
func (t *windowsWindowThread) destroyOverlay() {
	if t.overlay != 0 {
		hideAndDestroyWindow(t.overlay)
		t.overlay = 0
	}
	if t.overlayBmp != 0 {
		procDeleteObject.Call(t.overlayBmp)
		t.overlayBmp = 0
	}
}

// showThumbnail uploads bmp to the overlay window (creating it lazily) and shows
// it at x,y,w,h (physical pixels, parent client coords) raised above the video.
func (t *windowsWindowThread) showThumbnail(bmp *overlayBitmap, x, y, w, h int) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return t.call(ctx, func() error {
		if t.parent == 0 {
			overlayDebugf("showThumbnail: no parent window")
			return fmt.Errorf("native player: window not ready")
		}
		if t.overlay == 0 {
			hwnd, err := createSeekOverlayWindow(t.parent)
			if err != nil {
				overlayDebugf("createSeekOverlayWindow err=%v", err)
				return err
			}
			t.overlay = hwnd
			overlayDebugf("created overlay hwnd=%d parent=%d", hwnd, t.parent)
		}
		hbm, err := newOverlayDIB(bmp)
		if err != nil {
			overlayDebugf("newOverlayDIB err=%v", err)
			return err
		}
		procSendMessageW.Call(t.overlay, stmSetImage, imageBitmap, hbm)
		// The STATIC now references hbm; free the bitmap it held before.
		if t.overlayBmp != 0 {
			procDeleteObject.Call(t.overlayBmp)
		}
		t.overlayBmp = hbm
		positionSeekOverlay(t.overlay, x, y, w, h)
		return nil
	})
}

// moveThumbnail repositions the existing overlay. If no bitmap has been shown
// yet, it does nothing; the next showThumbnail call will create and paint it.
func (t *windowsWindowThread) moveThumbnail(x, y, w, h int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return t.call(ctx, func() error {
		if t.overlay == 0 || t.overlayBmp == 0 {
			return nil
		}
		positionSeekOverlay(t.overlay, x, y, w, h)
		return nil
	})
}

// hideThumbnail hides the overlay without destroying it, so the next scrub can
// reuse the window and bitmap.
func (t *windowsWindowThread) hideThumbnail() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return t.call(ctx, func() error {
		if t.overlay != 0 {
			procShowWindow.Call(t.overlay, swHide)
		}
		return nil
	})
}

func (t *windowsWindowThread) close() {
	if t == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = t.call(ctx, func() error {
		return errWindowThreadStop
	})
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
	}
}

func pumpWindowMessages() {
	var msg winMsg
	for {
		ret, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove)
		if ret == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func createKillOnCloseJob() (syscall.Handle, error) {
	handle, _, callErr := procCreateJobObjectW.Call(0, 0)
	if handle == 0 {
		return 0, callFailed("CreateJobObjectW", callErr)
	}

	info := jobObjectExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	ret, _, callErr := procSetInformationJobObject.Call(
		handle,
		jobObjectInfoClassExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ret == 0 {
		_ = syscall.CloseHandle(syscall.Handle(handle))
		return 0, callFailed("SetInformationJobObject", callErr)
	}
	return syscall.Handle(handle), nil
}

func assignProcessToJob(job syscall.Handle, process windows.Handle) error {
	ret, _, callErr := procAssignProcessToJobObject.Call(uintptr(job), uintptr(process))
	if ret == 0 {
		return callFailed("AssignProcessToJobObject", callErr)
	}
	return nil
}

type windowsMPVProcess struct {
	process windows.Handle
	thread  windows.Handle
	once    sync.Once
}

func startSuspendedMPV(ctx context.Context, mpvPath string, args []string) (*windowsMPVProcess, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	appName, err := windows.UTF16PtrFromString(mpvPath)
	if err != nil {
		return nil, fmt.Errorf("native player: encode mpv path: %w", err)
	}
	commandLine := windows.ComposeCommandLine(append([]string{mpvPath}, args...))
	commandLinePtr, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, fmt.Errorf("native player: encode mpv command line: %w", err)
	}

	var startup windows.StartupInfo
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESHOWWINDOW
	startup.ShowWindow = windows.SW_HIDE

	var info windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(
		appName,
		commandLinePtr,
		nil,
		nil,
		false,
		flags,
		nil,
		nil,
		&startup,
		&info,
	); err != nil {
		return nil, fmt.Errorf("native player: start mpv suspended: %w", err)
	}
	return &windowsMPVProcess{process: info.Process, thread: info.Thread}, nil
}

func (p *windowsMPVProcess) resume() error {
	if p == nil || p.thread == 0 {
		return nil
	}
	if _, err := windows.ResumeThread(p.thread); err != nil {
		return fmt.Errorf("native player: resume mpv: %w", err)
	}
	return nil
}

func (p *windowsMPVProcess) kill() {
	if p == nil || p.process == 0 {
		return
	}
	_ = windows.TerminateProcess(p.process, 1)
}

func (p *windowsMPVProcess) wait() error {
	if p == nil || p.process == 0 {
		return nil
	}
	_, waitErr := windows.WaitForSingleObject(p.process, windows.INFINITE)
	var exitCode uint32
	exitErr := windows.GetExitCodeProcess(p.process, &exitCode)
	p.close()
	if waitErr != nil {
		return fmt.Errorf("native player: wait for mpv: %w", waitErr)
	}
	if exitErr != nil {
		return fmt.Errorf("native player: read mpv exit code: %w", exitErr)
	}
	if exitCode != 0 {
		return fmt.Errorf("native player: mpv exited with status %d", exitCode)
	}
	return nil
}

func (p *windowsMPVProcess) close() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.thread != 0 {
			_ = windows.CloseHandle(p.thread)
			p.thread = 0
		}
		if p.process != 0 {
			_ = windows.CloseHandle(p.process)
			p.process = 0
		}
	})
}

// rectToWindowCoords converts a viewport rect measured by the frontend in CSS
// (device-independent) pixels into the physical pixels that CreateWindowExW and
// SetWindowPos expect for a child of parent.
//
// The app is per-monitor DPI aware (WebView2 requires it), so child HWNDs are
// positioned in physical pixels while getBoundingClientRect reports logical
// ones. We bridge that gap by scaling with the parent window's DPI exactly
// once. Reading the parent's per-monitor DPI keeps the mapping correct as the
// window moves between displays of different scale, and it also stays correct
// under DPI virtualization: an unaware window reports 96 (scale 1.0), matching
// its already-virtualized coordinate space.
func rectToWindowCoords(parent uintptr, rect Rect) (int, int, int, int) {
	scale := dpiScaleForWindow(parent)
	x := int(math.Round(math.Max(0, rect.X*scale)))
	y := int(math.Round(math.Max(0, rect.Y*scale)))
	w := int(math.Round(math.Max(1, rect.Width*scale)))
	h := int(math.Round(math.Max(1, rect.Height*scale)))
	return x, y, w, h
}

// dpiScaleForWindow returns hwnd's DPI scale factor (1.0 at 100%, 1.25 at 125%,
// and so on). It falls back to 1.0 whenever the DPI cannot be determined — a
// zero handle, a Windows build predating GetDpiForWindow (before 1607), or a
// failed call — so an unknown DPI degrades to unscaled rather than to a guess.
func dpiScaleForWindow(hwnd uintptr) float64 {
	const defaultDPI = 96.0
	if hwnd == 0 {
		return 1.0
	}
	if err := procGetDpiForWindow.Find(); err != nil {
		return 1.0
	}
	ret, _, _ := procGetDpiForWindow.Call(hwnd)
	if ret == 0 {
		return 1.0
	}
	return float64(ret) / defaultDPI
}

func windowVisible(hwnd uintptr) bool {
	ret, _, _ := procIsWindowVisible.Call(hwnd)
	return ret != 0
}

func windowProcessID(hwnd uintptr) uint32 {
	var pid uint32
	procGetWindowThreadProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func windowText(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buf := make([]uint16, int(length)+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), length+1)
	return syscall.UTF16ToString(buf)
}

func callFailed(name string, err error) error {
	if errno, ok := err.(syscall.Errno); ok && errno == 0 {
		return fmt.Errorf("native player: %s failed", name)
	}
	return fmt.Errorf("native player: %s failed: %w", name, err)
}

func windowsNativePlayerEnabled() bool {
	return nativePlayerEnabled(os.Getenv(windowsNativePlayerFlag))
}
