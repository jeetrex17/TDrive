package photobackup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const folderReadBatch = 128

type LocalFolderAdapter struct {
	MaxDepth      int
	ExcludedRoots []string
	mu            sync.Mutex
	sessions      map[string]*folderSession
}
type folderFrame struct {
	dir     *os.File
	path    string
	depth   int
	pending []os.DirEntry
	eof     bool
}
type folderSession struct {
	root  string
	stack []folderFrame
}

func NewLocalFolderAdapter(maxDepth int, excludedRoots []string) *LocalFolderAdapter {
	return &LocalFolderAdapter{MaxDepth: maxDepth, ExcludedRoots: append([]string(nil), excludedRoots...), sessions: make(map[string]*folderSession)}
}

// Close releases traversal handles when a worker stops between pages. Waiting
// for another Page call would retain open directories for every paused source.
func (a *LocalFolderAdapter) Close() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for token, scan := range a.sessions {
		a.close(token, scan)
	}
}

func (a *LocalFolderAdapter) Page(ctx context.Context, source Source, cursor string, limit int) (Page, error) {
	if a == nil || limit <= 0 || limit > 128 || source.Root == "" {
		return Page{}, ErrInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessions == nil {
		a.sessions = make(map[string]*folderSession)
	}
	token := cursor
	scan := a.sessions[token]
	if cursor == "" {
		var err error
		token, scan, err = a.start(source.Root)
		if err != nil {
			return Page{}, err
		}
		a.sessions[token] = scan
	} else if scan == nil {
		return Page{}, ErrCursorExpired
	}
	fail := func(err error) (Page, error) { a.close(token, scan); return Page{}, err }
	out := Page{}
	for len(out.Assets) < limit && len(scan.stack) > 0 {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		frameIndex := len(scan.stack) - 1
		top := &scan.stack[frameIndex]
		if len(top.pending) == 0 && !top.eof {
			entries, readErr := top.dir.ReadDir(folderReadBatch)
			if readErr != nil && readErr != io.EOF {
				return fail(fmt.Errorf("photobackup scan %q: %w", top.path, readErr))
			}
			top.pending = entries
			top.eof = readErr == io.EOF
		}
		descended := false
		for len(top.pending) > 0 {
			if len(out.Assets) == limit {
				break
			}
			entry := top.pending[0]
			top.pending = top.pending[1:]
			path := filepath.Join(top.path, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				if top.depth < a.depth() && !a.excluded(path) {
					dir, err := os.Open(path)
					if err != nil {
						return fail(err)
					}
					scan.stack = append(scan.stack, folderFrame{dir: dir, path: path, depth: top.depth + 1})
					descended = true
					break
				}
				continue
			}
			if !entry.Type().IsRegular() {
				continue
			}
			if top.depth+1 > a.depth() {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return fail(err)
			}
			rel, err := filepath.Rel(scan.root, path)
			if err != nil {
				return fail(err)
			}
			out.Assets = append(out.Assets, Asset{ID: filepath.ToSlash(rel), Version: strconv.FormatInt(info.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(info.Size(), 10), Path: path, Name: entry.Name(), ModifiedAt: info.ModTime(), Size: info.Size()})
		}
		if descended {
			continue
		}
		if top.eof && len(top.pending) == 0 {
			_ = top.dir.Close()
			scan.stack = append(scan.stack[:frameIndex], scan.stack[frameIndex+1:]...)
		}
	}
	if len(scan.stack) == 0 {
		a.close(token, scan)
		return out, nil
	}
	out.NextCursor = token
	return out, nil
}
func (a *LocalFolderAdapter) depth() int {
	if a.MaxDepth <= 0 {
		return 32
	}
	return a.MaxDepth
}
func (a *LocalFolderAdapter) start(root string) (string, *folderSession, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", nil, err
	}
	if a.excluded(abs) {
		return "", nil, fmt.Errorf("photobackup: source root is excluded")
	}
	dir, err := os.Open(abs)
	if err != nil {
		return "", nil, err
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		dir.Close()
		return "", nil, err
	}
	return hex.EncodeToString(raw[:]), &folderSession{root: abs, stack: []folderFrame{{dir: dir, path: abs}}}, nil
}
func (a *LocalFolderAdapter) close(token string, s *folderSession) {
	for _, f := range s.stack {
		_ = f.dir.Close()
	}
	delete(a.sessions, token)
}
func (a *LocalFolderAdapter) excluded(path string) bool {
	abs, _ := filepath.Abs(path)
	for _, root := range a.ExcludedRoots {
		x, _ := filepath.Abs(root)
		rel, err := filepath.Rel(x, abs)
		if err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}
