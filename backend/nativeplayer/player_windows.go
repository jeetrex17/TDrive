//go:build windows

package nativeplayer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
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
	procMoveWindow             = user32.NewProc("MoveWindow")
	procShowWindow             = user32.NewProc("ShowWindow")

	enumWindowsMu       sync.Mutex
	enumWindowsCallback = syscall.NewCallback(enumWindowsProc)
	enumTargetPID       uint32
	enumFirstWindow     uintptr
	enumTitledWindow    uintptr
)

type Player struct {
	mu      sync.Mutex
	closed  bool
	parent  uintptr
	child   uintptr
	ipcPath string
	proc    *windowsMPVProcess
	done    chan error
	job     syscall.Handle

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
	child, err := createVideoChildWindow(parent, rect)
	if err != nil {
		return nil, err
	}

	player := &Player{parent: parent, child: child, done: make(chan error, 1)}
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
		"--osc=yes",
		"--osd-bar=yes",
		"--force-window=immediate",
		"--input-terminal=no",
		"--input-ipc-server=" + p.ipcPath,
		fmt.Sprintf("--wid=%d", p.child),
		"--",
		url,
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
	go func() {
		err := proc.wait()
		p.done <- err
		p.destroyChild()
		p.closeJob()
	}()
	return nil
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
	parent := p.parent
	child := p.child
	closed := p.closed
	p.mu.Unlock()
	if closed || child == 0 {
		return nil
	}
	x, y, w, h := scaleRect(rect, parent)
	ret, _, callErr := procMoveWindow.Call(child, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 1)
	if ret == 0 {
		return callFailed("MoveWindow", callErr)
	}
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
	proc := p.proc
	done := p.done
	p.mu.Unlock()

	if ipcPath != "" {
		_ = writeMPVIPC(ipcPath, []byte(`{"command":["quit"]}`+"\n"))
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
	p.destroyChild()
	p.closeJob()
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

func windowsNativePlayerEnabled() bool {
	return os.Getenv(windowsNativePlayerFlag) == "1"
}
