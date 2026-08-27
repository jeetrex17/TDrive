package file

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const importWalkReadBatchSize = 256

type importWalkFrame struct {
	dir      *os.File
	fullPath string
	relPath  string
	entries  []os.DirEntry
	next     int
	readErr  error
}

// walkImportDescendants performs a depth-first walk without WalkDir's
// read-and-sort-entire-directory behavior. At most one small ReadDir window is
// retained per active directory, which keeps very wide project folders from
// causing a memory spike before the import limit can stop the scan.
func walkImportDescendants(
	ctx context.Context,
	root string,
	visit func(fullPath, relPath string, entry os.DirEntry) (skipDir bool, err error),
	onReadError func(path string, err error) error,
) (err error) {
	if ctx == nil {
		return errors.New("import walk requires a context")
	}
	rootDir, err := os.Open(root)
	if err != nil {
		return err
	}
	stack := []*importWalkFrame{{dir: rootDir, fullPath: root}}
	defer func() {
		for _, frame := range stack {
			_ = frame.dir.Close()
		}
	}()

	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame := stack[len(stack)-1]

		if frame.next >= len(frame.entries) {
			if frame.readErr != nil {
				readErr := frame.readErr
				_ = frame.dir.Close()
				stack = stack[:len(stack)-1]
				if !errors.Is(readErr, io.EOF) && onReadError != nil {
					if err := onReadError(frame.fullPath, readErr); err != nil {
						return err
					}
				}
				continue
			}

			frame.entries, frame.readErr = frame.dir.ReadDir(importWalkReadBatchSize)
			frame.next = 0
			if len(frame.entries) == 0 {
				continue
			}
		}

		entry := frame.entries[frame.next]
		frame.next++
		fullPath := filepath.Join(frame.fullPath, entry.Name())
		relPath := entry.Name()
		if frame.relPath != "" {
			relPath = filepath.Join(frame.relPath, entry.Name())
		}

		skipDir, err := visit(fullPath, filepath.ToSlash(relPath), entry)
		if err != nil {
			return err
		}
		if !entry.IsDir() || skipDir {
			continue
		}

		child, err := os.Open(fullPath)
		if err != nil {
			if onReadError != nil {
				if err := onReadError(fullPath, err); err != nil {
					return err
				}
			}
			continue
		}
		stack = append(stack, &importWalkFrame{
			dir:      child,
			fullPath: fullPath,
			relPath:  relPath,
		})
	}
	return nil
}
