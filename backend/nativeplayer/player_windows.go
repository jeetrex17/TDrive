//go:build windows

package nativeplayer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsClipChildren = 0x02000000
	wsClipSiblings = 0x04000000

	swHide        = 0
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")

	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procEnumWindows            = user32.NewProc("EnumWindows")
	procGetDpiForWindow        = user32.NewProc("GetDpiForWindow")
	procGetWindowTextLengthW   = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcess = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
	procMoveWindow             = user32.NewProc("MoveWindow")
	procSetWindowPos           = user32.NewProc("SetWindowPos")
	procShowWindow             = user32.NewProc("ShowWindow")
)

type Player struct {
	mu      sync.Mutex
	closed  bool
	child   uintptr
	ipcPath string
	cmd     *exec.Cmd
	done    chan error

	destroyOnce sync.Once
}

func Start(ctx context.Context, url string, rect Rect) (*Player, error) {
	if !rect.Valid() {
		return nil, fmt.Errorf("native player: invalid view rect")
	}

	parent, err := findTDriveWindow()
	if err != nil {
		return nil, err
	}
	child, err := createVideoChildWindow(parent, rect)
	if err != nil {
		return nil, err
	}

	player := &Player{child: child, done: make(chan error, 1)}
	if err := player.startProcess(ctx, url); err != nil {
		player.destroyChild()
		return nil, err
	}
	return player, nil
}

func (p *Player) startProcess(ctx context.Context, url string) error {
	mpvPath, err := findWindowsMPV()
	if err != nil {
		return err
	}

	p.ipcPath = fmt.Sprintf(`\\.\pipe\tdrive-mpv-%d-%d`, os.Getpid(), time.Now().UnixNano())
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
		fmt.Sprintf("--wid=%d", p.child),
		"--",
		url,
	}

	cmd := exec.CommandContext(ctx, mpvPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("native player: start mpv: %w", err)
	}
	p.cmd = cmd
	go func() {
		err := cmd.Wait()
		p.done <- err
		p.destroyChild()
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
	child := p.child
	closed := p.closed
	p.mu.Unlock()
	if closed || child == 0 {
		return nil
	}
	parent := uintptr(0)
	if hwnd, err := findTDriveWindow(); err == nil {
		parent = hwnd
	}
	x, y, w, h := scaleRect(rect, parent)
	ret, _, callErr := procMoveWindow.Call(child, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 1)
	if ret == 0 {
		return callFailed("MoveWindow", callErr)
	}
	procSetWindowPos.Call(child, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoZOrder|swpNoActivate)
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
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
	}
	p.destroyChild()
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
		p.mu.Unlock()
		if child != 0 {
			procShowWindow.Call(child, swHide)
			procDestroyWindow.Call(child)
		}
	})
}

func writeMPVIPC(path string, payload []byte) error {
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		file, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err == nil {
			_, writeErr := file.Write(payload)
			closeErr := file.Close()
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

func findTDriveWindow() (uintptr, error) {
	currentPID := uint32(os.Getpid())
	var first uintptr
	var titled uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		if !windowVisible(hwnd) || windowProcessID(hwnd) != currentPID {
			return 1
		}
		if first == 0 {
			first = hwnd
		}
		if strings.Contains(windowText(hwnd), "TDrive") {
			titled = hwnd
			return 0
		}
		return 1
	})
	ret, _, callErr := procEnumWindows.Call(cb, 0)
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

func createVideoChildWindow(parent uintptr, rect Rect) (uintptr, error) {
	x, y, w, h := scaleRect(rect, parent)
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
	return child, nil
}

func scaleRect(rect Rect, hwnd uintptr) (int, int, int, int) {
	scale := 1.0
	if hwnd != 0 {
		scale = float64(dpiForWindow(hwnd)) / 96.0
	}
	x := int(math.Round(math.Max(0, rect.X*scale)))
	y := int(math.Round(math.Max(0, rect.Y*scale)))
	w := int(math.Round(math.Max(1, rect.Width*scale)))
	h := int(math.Round(math.Max(1, rect.Height*scale)))
	return x, y, w, h
}

func dpiForWindow(hwnd uintptr) int {
	if err := procGetDpiForWindow.Find(); err != nil {
		return 96
	}
	ret, _, _ := procGetDpiForWindow.Call(hwnd)
	if ret == 0 {
		return 96
	}
	return int(ret)
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
