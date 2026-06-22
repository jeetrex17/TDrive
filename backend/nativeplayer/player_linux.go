//go:build linux

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

const linuxNativePlayerFlag = "TDRIVE_EXPERIMENTAL_LINUX_NATIVE_PLAYER"

type Player struct {
	mu      sync.Mutex
	closed  bool
	view    unsafe.Pointer
	ipcPath string
	cmd     *exec.Cmd
	done    chan error

	closeOnce sync.Once
}

func Start(ctx context.Context, url string, rect Rect) (*Player, error) {
	if !linuxNativePlayerEnabled() {
		return nil, ErrUnsupported
	}
	if os.Getenv("DISPLAY") == "" {
		return nil, fmt.Errorf("%w: Linux native playback currently requires X11", ErrUnsupported)
	}
	if !rect.Valid() {
		return nil, fmt.Errorf("native player: invalid view rect")
	}

	view := C.tdrive_x11_create(C.double(rect.X), C.double(rect.Y), C.double(rect.Width), C.double(rect.Height))
	if view == nil {
		return nil, fmt.Errorf("native player: could not create X11 child window")
	}
	child := uintptr(C.tdrive_x11_child(view))
	if child == 0 {
		C.tdrive_x11_destroy(view)
		return nil, fmt.Errorf("native player: invalid X11 child window")
	}

	player := &Player{view: unsafe.Pointer(view), done: make(chan error, 1)}
	if err := player.startProcess(ctx, url, child); err != nil {
		player.destroyView()
		return nil, err
	}
	return player, nil
}

func (p *Player) startProcess(ctx context.Context, url string, windowID uintptr) error {
	mpvPath, err := findLinuxMPV()
	if err != nil {
		return err
	}

	p.ipcPath = filepath.Join(os.TempDir(), fmt.Sprintf("tdrive-mpv-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	_ = os.Remove(p.ipcPath)
	args := []string{
		"--no-config",
		"--terminal=no",
		"--msg-level=all=warn",
		"--ytdl=no",
		"--hwdec=auto-safe",
		"--osc=yes",
		"--osd-bar=yes",
		"--force-window=immediate",
		"--input-terminal=no",
		"--input-ipc-server=" + p.ipcPath,
		fmt.Sprintf("--wid=%d", windowID),
		"--",
		url,
	}

	cmd := exec.CommandContext(ctx, mpvPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(p.ipcPath)
		return fmt.Errorf("native player: start mpv: %w", err)
	}
	p.cmd = cmd
	go func() {
		err := cmd.Wait()
		p.done <- err
		p.destroyView()
		_ = os.Remove(p.ipcPath)
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
	cmd := p.cmd
	done := p.done
	p.mu.Unlock()

	if ipcPath != "" {
		_ = writeMPVIPC(ipcPath, []byte(`{"command":["quit"]}`+"\n"))
	}
	if cmd != nil && cmd.Process != nil {
		select {
		case <-done:
		case <-time.After(750 * time.Millisecond):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
	}
	p.destroyView()
	_ = os.Remove(ipcPath)
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

func linuxNativePlayerEnabled() bool {
	return os.Getenv(linuxNativePlayerFlag) == "1"
}
