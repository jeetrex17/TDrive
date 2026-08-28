//go:build windows

package file

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsStagingNameAttempts = 32

type windowsFolderDownloadStaging struct {
	parentPath     string
	path           string
	parentHandle   windows.Handle
	stagingHandle  windows.Handle
	namespaceLocks []windows.Handle
}

type windowsFileRenameInformation struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

type windowsFileBasicInformation struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              uint32
}

func createPrivateFolderDownloadStaging(parentPath string) (folderDownloadStaging, error) {
	absoluteParent, err := filepath.Abs(parentPath)
	if err != nil {
		return nil, fmt.Errorf("resolve download destination: %w", err)
	}
	if err := validateDestinationParent(absoluteParent); err != nil {
		return nil, err
	}
	parentHandle, err := openWindowsDirectory(absoluteParent, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return nil, fmt.Errorf("lock download destination: %w", err)
	}
	resolvedParent, namespaceLocks, err := lockWindowsNamespace(parentHandle)
	if err != nil {
		_ = windows.CloseHandle(parentHandle)
		return nil, err
	}

	stagingName, stagingHandle, err := createPrivateWindowsStagingDirectory(parentHandle)
	if err != nil {
		closeWindowsHandles(namespaceLocks)
		_ = windows.CloseHandle(parentHandle)
		return nil, err
	}
	stagingPath := filepath.Join(resolvedParent, stagingName)
	return &windowsFolderDownloadStaging{
		parentPath: resolvedParent, path: stagingPath,
		parentHandle: parentHandle, stagingHandle: stagingHandle,
		namespaceLocks: namespaceLocks,
	}, nil
}

func openWindowsDirectory(path string, access uint32) (windows.Handle, error) {
	pathPtr, err := windows.UTF16PtrFromString(windowsExtendedPath(path))
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(
		pathPtr,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
}

func lockWindowsNamespace(parentHandle windows.Handle) (string, []windows.Handle, error) {
	resolvedParent, err := finalWindowsPath(parentHandle)
	if err != nil {
		return "", nil, fmt.Errorf("resolve locked download destination: %w", err)
	}
	resolvedParent = windowsDisplayPath(resolvedParent)
	volumeRoot := filepath.Clean(filepath.VolumeName(resolvedParent) + `\`)
	handles := make([]windows.Handle, 0, 8)
	for current := filepath.Dir(resolvedParent); ; current = filepath.Dir(current) {
		current = filepath.Clean(current)
		if current == "." || strings.EqualFold(current, volumeRoot) {
			break
		}
		handle, err := openWindowsDirectory(current, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES)
		if err != nil {
			closeWindowsHandles(handles)
			return "", nil, fmt.Errorf("lock download destination namespace %q: %w", current, err)
		}
		handles = append(handles, handle)
		next := filepath.Dir(current)
		if strings.EqualFold(next, current) {
			break
		}
	}
	confirmedParent, err := finalWindowsPath(parentHandle)
	if err != nil {
		closeWindowsHandles(handles)
		return "", nil, fmt.Errorf("confirm locked download destination: %w", err)
	}
	confirmedParent = windowsDisplayPath(confirmedParent)
	if !strings.EqualFold(filepath.Clean(confirmedParent), filepath.Clean(resolvedParent)) {
		closeWindowsHandles(handles)
		return "", nil, fmt.Errorf("download destination changed while securing its namespace")
	}
	return resolvedParent, handles, nil
}

func finalWindowsPath(handle windows.Handle) (string, error) {
	size := uint32(512)
	for {
		buffer := make([]uint16, size)
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], size, 0)
		if err != nil {
			return "", err
		}
		if length < size {
			return windows.UTF16ToString(buffer[:length]), nil
		}
		size = length + 1
	}
}

func windowsDisplayPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}

func closeWindowsHandles(handles []windows.Handle) {
	for _, handle := range handles {
		_ = windows.CloseHandle(handle)
	}
}

func createPrivateWindowsStagingDirectory(parentHandle windows.Handle) (string, windows.Handle, error) {
	descriptor, err := privateWindowsDirectorySecurityDescriptor()
	if err != nil {
		return "", windows.InvalidHandle, err
	}
	for range windowsStagingNameAttempts {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", windows.InvalidHandle, fmt.Errorf("generate staging directory name: %w", err)
		}
		name := ".tdrive-folder-download-" + hex.EncodeToString(random[:])
		objectName, err := windows.NewNTUnicodeString(name)
		if err != nil {
			return "", windows.InvalidHandle, err
		}
		attributes := &windows.OBJECT_ATTRIBUTES{
			Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
			RootDirectory:      parentHandle,
			ObjectName:         objectName,
			Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
			SecurityDescriptor: descriptor,
		}
		var (
			handle windows.Handle
			status windows.IO_STATUS_BLOCK
		)
		err = windows.NtCreateFile(
			&handle,
			windows.DELETE|windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|
				windows.FILE_WRITE_ATTRIBUTES|windows.SYNCHRONIZE,
			attributes,
			&status,
			nil,
			windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_ATTRIBUTE_TEMPORARY,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			windows.FILE_CREATE,
			windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
			0,
			0,
		)
		if err == windows.STATUS_OBJECT_NAME_COLLISION {
			continue
		}
		if err != nil {
			return "", windows.InvalidHandle, fmt.Errorf("create private staging directory: %w", err)
		}
		return name, handle, nil
	}
	return "", windows.InvalidHandle, fmt.Errorf("create private staging directory: name collisions exhausted")
}

func privateWindowsDirectorySecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("resolve current Windows user: %w", err)
	}
	userSID := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
		"O:%sD:P(A;OICI;FA;;;%s)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
		userSID,
		userSID,
	))
	if err != nil {
		return nil, fmt.Errorf("create private staging ACL: %w", err)
	}
	return descriptor, nil
}

func (s *windowsFolderDownloadStaging) ParentPath() string {
	return s.parentPath
}

func (s *windowsFolderDownloadStaging) Path() string {
	return s.path
}

func (s *windowsFolderDownloadStaging) PublishNoReplace(finalPath string) error {
	relativePath, err := filepath.Rel(s.parentPath, finalPath)
	if err != nil || relativePath == "." || filepath.Dir(relativePath) != "." {
		return fmt.Errorf("publish folder outside selected destination: %q", finalPath)
	}
	originalAttributes, err := clearWindowsStagingAttributes(s.stagingHandle)
	if err != nil {
		return err
	}
	if err := renameOpenWindowsDirectoryNoReplace(s.stagingHandle, s.parentHandle, relativePath); err != nil {
		_ = setWindowsStagingAttributes(s.stagingHandle, originalAttributes)
		return &os.LinkError{Op: "rename", Old: s.path, New: finalPath, Err: err}
	}
	return nil
}

func clearWindowsStagingAttributes(handle windows.Handle) (uint32, error) {
	var info windowsFileBasicInformation
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return 0, err
	}
	original := info.FileAttributes
	info.FileAttributes &^= windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_TEMPORARY
	if err := windows.SetFileInformationByHandle(
		handle,
		windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return 0, err
	}
	return original, nil
}

func setWindowsStagingAttributes(handle windows.Handle, attributes uint32) error {
	var info windowsFileBasicInformation
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return err
	}
	info.FileAttributes = attributes
	return windows.SetFileInformationByHandle(
		handle,
		windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
}

func renameOpenWindowsDirectoryNoReplace(
	handle windows.Handle,
	parentHandle windows.Handle,
	finalName string,
) error {
	name, err := windows.UTF16FromString(finalName)
	if err != nil {
		return err
	}
	name = name[:len(name)-1]
	var layout windowsFileRenameInformation
	bufferSize := int(unsafe.Offsetof(layout.FileName)) + len(name)*2
	buffer := make([]byte, bufferSize)
	info := (*windowsFileRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.RootDirectory = parentHandle
	info.FileNameLength = uint32(len(name) * 2)
	copy(unsafe.Slice(&info.FileName[0], len(name)), name)
	return windows.SetFileInformationByHandle(
		handle,
		windows.FileRenameInfo,
		&buffer[0],
		uint32(len(buffer)),
	)
}

func (s *windowsFolderDownloadStaging) PrepareCleanup() error {
	return closeWindowsHandle(&s.stagingHandle)
}

func (s *windowsFolderDownloadStaging) Close() error {
	errs := []error{
		closeWindowsHandle(&s.stagingHandle),
		closeWindowsHandle(&s.parentHandle),
	}
	for i := range s.namespaceLocks {
		errs = append(errs, closeWindowsHandle(&s.namespaceLocks[i]))
	}
	s.namespaceLocks = nil
	return errors.Join(errs...)
}

func closeWindowsHandle(handle *windows.Handle) error {
	if *handle == 0 || *handle == windows.InvalidHandle {
		return nil
	}
	err := windows.CloseHandle(*handle)
	*handle = windows.InvalidHandle
	return err
}
