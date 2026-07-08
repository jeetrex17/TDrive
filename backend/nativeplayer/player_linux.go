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
			int contains = strstr((const char *)data, needle) != NULL;
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
	"bufio"
	"context"
	"encoding/json"
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
	mu          sync.Mutex
	closed      bool
	view        unsafe.Pointer
	ipcPath     string
	ipcDir      string
	cmd         *exec.Cmd
	done        chan error
	stateCancel context.CancelFunc
	stateDone   chan struct{}

	closeOnce sync.Once
}

func Start(ctx context.Context, url string, rect Rect, opts Options) (*Player, error) {
	if !linuxNativePlayerEnabled() {
		linuxNativeLogf("start rejected: disabled by %s=0", linuxNativePlayerFlag)
		return nil, ErrUnsupported
	}
	if os.Getenv("DISPLAY") == "" {
		linuxNativeLogf("start rejected: DISPLAY is empty (session=%s wayland=%s)", os.Getenv("XDG_SESSION_TYPE"), os.Getenv("WAYLAND_DISPLAY"))
		return nil, fmt.Errorf("%w: Linux native playback currently requires X11", ErrUnsupported)
	}
	if !rect.Valid() {
		linuxNativeLogf("start rejected: invalid rect x=%.1f y=%.1f w=%.1f h=%.1f", rect.X, rect.Y, rect.Width, rect.Height)
		return nil, fmt.Errorf("native player: invalid view rect")
	}

	linuxNativeLogf(
		"start requested: display=%s session=%s gdk_backend=%s rect=x%.1f y%.1f w%.1f h%.1f html_controls=%t",
		os.Getenv("DISPLAY"),
		os.Getenv("XDG_SESSION_TYPE"),
		os.Getenv("GDK_BACKEND"),
		rect.X,
		rect.Y,
		rect.Width,
		rect.Height,
		opts.UseHTMLControls,
	)
	view := C.tdrive_x11_create(C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height))
	if view == nil {
		linuxNativeLogf("start failed: could not create X11 child window")
		return nil, fmt.Errorf("native player: could not create X11 child window")
	}
	child := uintptr(C.tdrive_x11_child(view))
	if child == 0 {
		C.tdrive_x11_destroy(view)
		linuxNativeLogf("start failed: X11 child window id is zero")
		return nil, fmt.Errorf("native player: invalid X11 child window")
	}
	linuxNativeLogf("x11 child ready: wid=%d", child)

	player := &Player{view: unsafe.Pointer(view), done: make(chan error, 1)}
	if err := player.startProcess(ctx, url, child, opts); err != nil {
		player.destroyView()
		linuxNativeLogf("start failed: %v", err)
		return nil, err
	}
	linuxNativeLogf("start ok: wid=%d", child)
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
		"--input-ipc-server=" + p.ipcPath,
		fmt.Sprintf("--wid=%d", windowID),
		"--",
		url,
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
	if opts.OnState != nil {
		stateCtx, cancel := context.WithCancel(ctx)
		p.stateCancel = cancel
		p.stateDone = make(chan struct{})
		go p.pollState(stateCtx, opts.OnState)
	}
	go func() {
		err := cmd.Wait()
		if err != nil {
			linuxNativeLogf("mpv exited: %v", err)
		} else {
			linuxNativeLogf("mpv exited cleanly")
		}
		p.mu.Lock()
		stateCancel := p.stateCancel
		p.mu.Unlock()
		if stateCancel != nil {
			stateCancel()
		}
		p.done <- err
		p.destroyView()
		_ = os.RemoveAll(p.ipcDir)
	}()
	return nil
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

	payload, err := json.Marshal(struct {
		Command []string `json:"command"`
	}{Command: command})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return writeMPVIPC(ipcPath, payload)
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
	stateDone := p.stateDone
	p.mu.Unlock()

	linuxNativeLogf("close requested")
	if stateCancel != nil {
		stateCancel()
	}
	if ipcPath != "" {
		_ = writeMPVIPC(ipcPath, []byte(`{"command":["quit"]}`+"\n"))
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
	if stateDone != nil {
		select {
		case <-stateDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	p.destroyView()
	_ = os.RemoveAll(ipcDir)
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
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			_, writeErr := conn.Write(payload)
			closeErr := conn.Close()
			if writeErr != nil {
				return writeErr
			}
			return closeErr
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("native player: mpv IPC unavailable: %w", lastErr)
}

func (p *Player) pollState(ctx context.Context, onState StateHandler) {
	defer close(p.stateDone)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		p.mu.Lock()
		ipcPath := p.ipcPath
		closed := p.closed
		p.mu.Unlock()
		if closed || ipcPath == "" {
			return
		}
		state, ok := readLinuxMPVState(ipcPath)
		if ok {
			onState(state)
		}
	}
}

func readLinuxMPVState(ipcPath string) (State, bool) {
	values, err := queryLinuxMPVProperties(ipcPath, []string{
		"time-pos",
		"duration",
		"pause",
		"mute",
		"volume",
		"speed",
		"paused-for-cache",
		"cache-buffering-state",
		"demuxer-cache-duration",
	})
	if err != nil {
		return State{}, false
	}

	current := cleanLinuxSeconds(linuxFloatProperty(values, "time-pos"))
	duration := cleanLinuxSeconds(linuxFloatProperty(values, "duration"))
	paused := linuxBoolProperty(values, "pause")
	muted := linuxBoolProperty(values, "mute")
	volume := clampLinuxFloat(linuxFloatProperty(values, "volume")/100, 0, 1)
	rate := clampLinuxFloat(linuxFloatProperty(values, "speed"), 0.25, 4)
	if rate == 0 {
		rate = 1
	}
	buffering := linuxFloatProperty(values, "cache-buffering-state")
	state := State{
		Paused:      paused,
		CurrentTime: clampLinuxFloat(current, 0, maxLinuxFloat(duration, current)),
		Duration:    duration,
		Volume:      volume,
		Muted:       muted || volume == 0,
		Rate:        rate,
		Loading:     linuxBoolProperty(values, "paused-for-cache") || (!paused && buffering > 0 && buffering < 100),
	}
	cacheDuration := cleanLinuxSeconds(linuxFloatProperty(values, "demuxer-cache-duration"))
	if duration > 0 && cacheDuration > 0 {
		state.Buffered = []BufferedRange{{
			Start: clampLinuxFloat(state.CurrentTime, 0, duration),
			End:   clampLinuxFloat(state.CurrentTime+cacheDuration, 0, duration),
		}}
	}
	return state, true
}

type linuxMPVIPCRequest struct {
	Command   []string `json:"command"`
	RequestID int      `json:"request_id"`
}

type linuxMPVIPCResponse struct {
	RequestID int             `json:"request_id"`
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
}

func queryLinuxMPVProperties(path string, names []string) (map[string]any, error) {
	conn, err := openLinuxMPVIPCReadWrite(path)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))

	encoder := json.NewEncoder(conn)
	idToName := make(map[int]string, len(names))
	for i, name := range names {
		id := i + 1
		idToName[id] = name
		if err := encoder.Encode(linuxMPVIPCRequest{Command: []string{"get_property", name}, RequestID: id}); err != nil {
			return nil, err
		}
	}

	values := make(map[string]any, len(names))
	scanner := bufio.NewScanner(conn)
	for len(idToName) > 0 && scanner.Scan() {
		var response linuxMPVIPCResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			continue
		}
		name, ok := idToName[response.RequestID]
		if !ok {
			continue
		}
		delete(idToName, response.RequestID)
		if response.Error != "success" {
			continue
		}
		var value any
		if err := json.Unmarshal(response.Data, &value); err != nil {
			continue
		}
		values[name] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func openLinuxMPVIPCReadWrite(path string) (net.Conn, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	return nil, fmt.Errorf("native player: mpv IPC unavailable: %w", lastErr)
}

func linuxFloatProperty(values map[string]any, key string) float64 {
	value, ok := values[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case json.Number:
		n, _ := typed.Float64()
		return n
	default:
		return 0
	}
}

func linuxBoolProperty(values map[string]any, key string) bool {
	value, ok := values[key]
	if !ok || value == nil {
		return false
	}
	typed, ok := value.(bool)
	return ok && typed
}

func cleanLinuxSeconds(value float64) float64 {
	if value < 0 || value != value {
		return 0
	}
	return value
}

func clampLinuxFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func maxLinuxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
