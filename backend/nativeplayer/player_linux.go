//go:build linux && cgo

package nativeplayer

/*
#cgo linux pkg-config: x11

#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

typedef struct {
	Display *display;
	Window parent;
	Window child;
} tdrive_x11_view;

__attribute__((constructor))
static void tdrive_x11_init_threads(void) {
	XInitThreads();
}

static int tdrive_x11_window_viewable(Display *display, Window window) {
	XWindowAttributes attrs;
	if (XGetWindowAttributes(display, window, &attrs) == 0) {
		return 0;
	}
	return attrs.map_state == IsViewable;
}

static int tdrive_x11_window_pid(Display *display, Window window) {
	Atom pid_atom = XInternAtom(display, "_NET_WM_PID", True);
	if (pid_atom == None) {
		return 0;
	}

	Atom actual_type;
	int actual_format;
	unsigned long nitems;
	unsigned long bytes_after;
	unsigned char *data = NULL;
	int status = XGetWindowProperty(
		display,
		window,
		pid_atom,
		0,
		1,
		False,
		XA_CARDINAL,
		&actual_type,
		&actual_format,
		&nitems,
		&bytes_after,
		&data
	);
	if (status != Success || data == NULL || nitems == 0) {
		if (data != NULL) {
			XFree(data);
		}
		return 0;
	}
	long pid = ((long *)data)[0];
	XFree(data);
	return (int)pid;
}

static int tdrive_bytes_contains(const unsigned char *value, unsigned long value_length, const char *needle) {
	if (value == NULL || needle == NULL) {
		return 0;
	}
	size_t needle_length = strlen(needle);
	if (needle_length == 0 || value_length < needle_length) {
		return 0;
	}
	for (unsigned long index = 0; index <= value_length - needle_length; index++) {
		if (memcmp(value + index, needle, needle_length) == 0) {
			return 1;
		}
	}
	return 0;
}

static int tdrive_x11_title_contains(Display *display, Window window, const char *needle) {
	if (needle == NULL || needle[0] == '\0') {
		return 0;
	}

	Atom utf8 = XInternAtom(display, "UTF8_STRING", True);
	Atom net_name = XInternAtom(display, "_NET_WM_NAME", True);
	if (utf8 != None && net_name != None) {
		Atom actual_type;
		int actual_format;
		unsigned long nitems;
		unsigned long bytes_after;
		unsigned char *data = NULL;
		int status = XGetWindowProperty(
			display,
			window,
			net_name,
			0,
			1024,
			False,
			utf8,
			&actual_type,
			&actual_format,
			&nitems,
			&bytes_after,
			&data
		);
		if (status == Success && data != NULL) {
			int contains = actual_format == 8 && tdrive_bytes_contains(data, nitems, needle);
			XFree(data);
			if (contains) {
				return 1;
			}
		}
	}

	char *wm_name = NULL;
	if (XFetchName(display, window, &wm_name) != 0 && wm_name != NULL) {
		int contains = strstr(wm_name, needle) != NULL;
		XFree(wm_name);
		return contains;
	}
	return 0;
}

static Window tdrive_x11_find_window(Display *display) {
	Window root = DefaultRootWindow(display);
	Atom client_list_atom = XInternAtom(display, "_NET_CLIENT_LIST", True);
	Window preferred = 0;
	Window first = 0;
	Window *windows = NULL;
	unsigned long count = 0;

	if (client_list_atom != None) {
		Atom actual_type;
		int actual_format;
		unsigned long bytes_after;
		unsigned char *data = NULL;
		int status = XGetWindowProperty(
			display,
			root,
			client_list_atom,
			0,
			4096,
			False,
			XA_WINDOW,
			&actual_type,
			&actual_format,
			&count,
			&bytes_after,
			&data
		);
		if (status == Success && data != NULL && count > 0) {
			windows = (Window *)data;
		}
	}

	if (windows == NULL) {
		Window root_return;
		Window parent_return;
		Window *children = NULL;
		unsigned int child_count = 0;
		if (XQueryTree(display, root, &root_return, &parent_return, &children, &child_count) == 0) {
			return 0;
		}
		windows = children;
		count = child_count;
	}

	int pid = (int)getpid();
	for (unsigned long i = 0; i < count; i++) {
		Window candidate = windows[i];
		if (!tdrive_x11_window_viewable(display, candidate)) {
			continue;
		}
		if (tdrive_x11_window_pid(display, candidate) != pid) {
			continue;
		}
		if (first == 0) {
			first = candidate;
		}
		if (tdrive_x11_title_contains(display, candidate, "TDrive")) {
			preferred = candidate;
			break;
		}
	}

	if (windows != NULL) {
		XFree(windows);
	}
	return preferred != 0 ? preferred : first;
}

static int tdrive_positive(double value) {
	if (value < 1) {
		return 1;
	}
	return (int)(value + 0.5);
}

static int tdrive_nonnegative(double value) {
	if (value < 0) {
		return 0;
	}
	return (int)(value + 0.5);
}

static tdrive_x11_view* tdrive_x11_create(double x, double y, double w, double h) {
	Display *display = XOpenDisplay(NULL);
	if (display == NULL) {
		return NULL;
	}

	Window parent = tdrive_x11_find_window(display);
	if (parent == 0) {
		XCloseDisplay(display);
		return NULL;
	}

	int screen = DefaultScreen(display);
	Window child = XCreateSimpleWindow(
		display,
		parent,
		tdrive_nonnegative(x),
		tdrive_nonnegative(y),
		tdrive_positive(w),
		tdrive_positive(h),
		0,
		BlackPixel(display, screen),
		BlackPixel(display, screen)
	);
	if (child == 0) {
		XCloseDisplay(display);
		return NULL;
	}

	XMapRaised(display, child);
	XFlush(display);

	tdrive_x11_view *view = (tdrive_x11_view *)calloc(1, sizeof(tdrive_x11_view));
	if (view == NULL) {
		XDestroyWindow(display, child);
		XCloseDisplay(display);
		return NULL;
	}
	view->display = display;
	view->parent = parent;
	view->child = child;
	return view;
}

static unsigned long tdrive_x11_child(tdrive_x11_view *view) {
	if (view == NULL) {
		return 0;
	}
	return (unsigned long)view->child;
}

static void tdrive_x11_resize(tdrive_x11_view *view, double x, double y, double w, double h) {
	if (view == NULL || view->display == NULL || view->child == 0) {
		return;
	}
	XMoveResizeWindow(
		view->display,
		view->child,
		tdrive_nonnegative(x),
		tdrive_nonnegative(y),
		tdrive_positive(w),
		tdrive_positive(h)
	);
	XFlush(view->display);
}

static void tdrive_x11_destroy(tdrive_x11_view *view) {
	if (view == NULL) {
		return;
	}
	if (view->display != NULL) {
		if (view->child != 0) {
			XUnmapWindow(view->display, view->child);
			XDestroyWindow(view->display, view->child);
			view->child = 0;
		}
		XFlush(view->display);
		XCloseDisplay(view->display);
		view->display = NULL;
	}
	free(view);
}
*/
import "C"

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type Player struct {
	mu            sync.Mutex
	closed        bool
	terminal      bool
	view          unsafe.Pointer
	ipcPath       string
	ipcDir        string
	cmd           *exec.Cmd
	done          chan error
	standalone    bool
	stateCancel   context.CancelFunc
	eventConn     net.Conn
	eventDone     chan struct{}
	onState       StateHandler
	lastState     State
	observedState *mpvObservedProperties

	closeOnce sync.Once
}

func Start(ctx context.Context, url string, rect Rect, opts Options) (*Player, error) {
	if !linuxNativePlayerEnabled() {
		linuxNativeLogf("start rejected: explicit opt-in required with %s=1", linuxNativePlayerFlag)
		return nil, ErrUnsupported
	}
	if !rect.Valid() {
		linuxNativeLogf("start rejected: invalid rect x=%.1f y=%.1f w=%.1f h=%.1f", rect.X, rect.Y, rect.Width, rect.Height)
		return nil, fmt.Errorf("native player: invalid view rect")
	}

	displayMode := selectLinuxDisplayMode(
		os.Getenv("GDK_BACKEND"),
		os.Getenv("XDG_SESSION_TYPE"),
		os.Getenv("WAYLAND_DISPLAY"),
		os.Getenv("DISPLAY"),
	)
	if displayMode == linuxDisplayUnavailable {
		linuxNativeLogf("start rejected: no supported graphical session")
		return nil, fmt.Errorf("%w: Linux native playback requires X11 or Wayland", ErrUnsupported)
	}

	linuxNativeLogf(
		"start requested: mode=%s rect=x%.1f y%.1f w%.1f h%.1f html_controls=%t",
		displayMode,
		rect.X,
		rect.Y,
		rect.Width,
		rect.Height,
		opts.UseHTMLControls,
	)
	var view *C.tdrive_x11_view
	var child uintptr
	if displayMode == linuxDisplayX11Embedded {
		view = C.tdrive_x11_create(C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height))
		if view == nil {
			linuxNativeLogf("start failed: could not create X11 child window")
			return nil, fmt.Errorf("native player: could not create X11 child window")
		}
		child = uintptr(C.tdrive_x11_child(view))
		if child == 0 {
			C.tdrive_x11_destroy(view)
			linuxNativeLogf("start failed: X11 child window id is zero")
			return nil, fmt.Errorf("native player: invalid X11 child window")
		}
		linuxNativeLogf("x11 child ready: wid=%d", child)
	}

	player := &Player{
		view:       unsafe.Pointer(view),
		done:       make(chan error, 1),
		standalone: displayMode == linuxDisplayWaylandStandalone,
	}
	if err := player.startProcess(ctx, url, child, opts); err != nil {
		player.destroyView()
		linuxNativeLogf("start failed: %v", err)
		return nil, err
	}
	linuxNativeLogf("start ok: mode=%s", displayMode)
	return player, nil
}

func (p *Player) startProcess(ctx context.Context, url string, windowID uintptr, opts Options) error {
	mpvPath, err := findLinuxMPV()
	if err != nil {
		linuxNativeLogf("mpv lookup failed: %v", err)
		return err
	}

	ipcDir, err := os.MkdirTemp("", "tdrive-mpv-*")
	if err != nil {
		return fmt.Errorf("native player: create IPC dir: %w", err)
	}
	_ = os.Chmod(ipcDir, 0o700)
	p.ipcDir = ipcDir
	p.ipcPath = filepath.Join(ipcDir, fmt.Sprintf("mpv-%d.sock", os.Getpid()))
	_ = os.Remove(p.ipcPath)
	args := []string{
		"--no-config",
		"--terminal=no",
		"--msg-level=all=warn",
		"--ytdl=no",
		"--hwdec=auto-safe",
		"--cache=yes",
		"--demuxer-readahead-secs=20",
		"--demuxer-max-bytes=67108864",
		"--demuxer-max-back-bytes=33554432",
		"--keepaspect=yes",
		"--force-window=immediate",
		"--input-terminal=no",
		"--idle=yes",
		"--keep-open=yes",
		"--input-ipc-server=" + p.ipcPath,
	}
	if windowID != 0 {
		args = append(args,
			"--keepaspect-window=no",
			"--auto-window-resize=no",
			"--video-align-x=0",
			"--video-align-y=0",
			"--osc=no",
			"--osd-bar=no",
			"--osd-level=0",
			"--cursor-autohide=no",
			"--no-input-default-bindings",
			"--input-vo-keyboard=no",
			fmt.Sprintf("--wid=%d", windowID),
		)
	} else {
		// Wayland does not provide the cross-process child-window embedding
		// primitive used on X11. Keep playback reliable in an honest standalone
		// mpv window and leave its native controls enabled.
		args = append(args,
			"--title=TDrive Video",
			"--osc=yes",
			"--osd-bar=yes",
			"--osd-level=1",
			"--input-default-bindings=yes",
			"--input-vo-keyboard=yes",
		)
	}

	cmd := exec.CommandContext(ctx, mpvPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	linuxNativeLogf("mpv start: path=%s wid=%d ipc=%s", mpvPath, windowID, p.ipcPath)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(p.ipcDir)
		linuxNativeLogf("mpv start failed: %v", err)
		return fmt.Errorf("native player: start mpv: %w", err)
	}
	p.cmd = cmd
	p.onState = opts.OnState
	if opts.OnState != nil {
		p.emitState(normalizeState(State{Status: StatusOpening, Paused: true, Loading: true, Volume: 1, Rate: 1}))
	}
	eventConn, err := dialLinuxMPVIPCWithAttempts(p.ipcPath, 80)
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		p.cmd = nil
		_ = os.RemoveAll(p.ipcDir)
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
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			p.cmd = nil
			_ = os.RemoveAll(p.ipcDir)
			return fmt.Errorf("native player: observe mpv state: %w", err)
		}
		_ = eventConn.SetWriteDeadline(time.Time{})
	}
	go p.observeEvents(stateCtx, eventConn, p.eventDone)
	if err := writeMPVIPCWithAttempts(p.ipcPath, mpvCommandPayload("loadfile", url, "replace"), 80); err != nil {
		cancel()
		_ = eventConn.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		p.cmd = nil
		_ = os.RemoveAll(p.ipcDir)
		return fmt.Errorf("native player: initialize mpv IPC: %w", err)
	}
	go func() {
		err := cmd.Wait()
		linuxNativeLogf("mpv process exited")
		p.handleProcessExit(err)
		p.done <- err
		p.destroyView()
		_ = os.RemoveAll(p.ipcDir)
	}()
	return nil
}

func (p *Player) Presentation() Presentation {
	if p != nil && p.standalone {
		return PresentationStandalone
	}
	return PresentationEmbedded
}

func (p *Player) Resize(rect Rect) error {
	if p == nil {
		return nil
	}
	if !rect.Valid() {
		return fmt.Errorf("native player: invalid view rect")
	}

	p.mu.Lock()
	view := p.view
	closed := p.closed
	if closed || view == nil {
		p.mu.Unlock()
		return nil
	}
	C.tdrive_x11_resize((*C.tdrive_x11_view)(view), C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height))
	p.mu.Unlock()
	return nil
}

// ShowSeekThumbnail and HideSeekThumbnail are no-ops on Linux: the native
// seek-preview overlay is currently implemented only on Windows. The methods
// exist so the cross-platform app layer can call them uniformly.
func (p *Player) ShowSeekThumbnail(_ []byte, _ Rect) error { return nil }

func (p *Player) MoveSeekThumbnail(_ Rect) error { return nil }

func (p *Player) HideSeekThumbnail() error { return nil }

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
	ipcDir := p.ipcDir
	cmd := p.cmd
	done := p.done
	stateCancel := p.stateCancel
	eventConn := p.eventConn
	eventDone := p.eventDone
	p.mu.Unlock()

	linuxNativeLogf("close requested")
	if ipcPath != "" {
		_ = writeMPVIPC(ipcPath, []byte(`{"command":["quit"]}`+"\n"))
	}
	if stateCancel != nil {
		stateCancel()
	}
	if eventConn != nil {
		_ = eventConn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		select {
		case <-done:
		case <-time.After(750 * time.Millisecond):
			linuxNativeLogf("close timeout: killing mpv process group pid=%d", cmd.Process.Pid)
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Process.Kill()
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
	p.destroyView()
	_ = os.RemoveAll(ipcDir)
	p.emitTerminal(StatusClosed)
	linuxNativeLogf("close complete")
	return nil
}

func (p *Player) destroyView() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		p.mu.Lock()
		view := p.view
		p.view = nil
		if view != nil {
			C.tdrive_x11_destroy((*C.tdrive_x11_view)(view))
			linuxNativeLogf("x11 child destroyed")
		}
		p.mu.Unlock()
	})
}

func writeMPVIPC(path string, payload []byte) error {
	return writeMPVIPCWithAttempts(path, payload, 20)
}

func writeMPVIPCWithAttempts(path string, payload []byte, attempts int) error {
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			_ = conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
			return writeAndCloseWithTimeout(conn, payload, 600*time.Millisecond)
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
	standalone := p.standalone
	stateCancel := p.stateCancel
	eventConn := p.eventConn
	p.mu.Unlock()
	if stateCancel != nil {
		stateCancel()
	}
	if eventConn != nil {
		_ = eventConn.Close()
	}
	switch sidecarExitStatus(closed, standalone, err) {
	case StatusClosed:
		p.emitTerminal(StatusClosed)
	default:
		p.emitTerminal(StatusFailed)
	}
}

func (p *Player) observeEvents(ctx context.Context, conn net.Conn, done chan<- struct{}) {
	defer close(done)
	defer conn.Close()
	stopClose := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopClose:
		}
	}()
	defer close(stopClose)
	if err := scanMPVEvents(conn, func(event mpvIPCEvent) {
		p.handleMPVEvent(event)
	}); err != nil && ctx.Err() == nil {
		linuxNativeLogf("mpv event observer stopped: %v", err)
	}
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

func dialLinuxMPVIPCWithAttempts(path string, attempts int) (net.Conn, error) {
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	return nil, fmt.Errorf("native player: mpv IPC unavailable: %w", lastErr)
}
