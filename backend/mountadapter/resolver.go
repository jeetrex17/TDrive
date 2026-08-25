package mountadapter

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"TDrive/backend/mountdav"
	"TDrive/backend/mountfs"
	"TDrive/backend/projection"
)

type projectionResolver struct {
	db      *sql.DB
	driveID int64
}

func NewProjectionResolver(db *sql.DB, driveID int64) (Resolver, error) {
	if db == nil || driveID <= 0 {
		return nil, mountdav.ErrWriteUnavailable
	}
	return projectionResolver{db: db, driveID: driveID}, nil
}

func (resolver projectionResolver) Resolve(ctx context.Context, value string) (Node, bool, error) {
	if ctx == nil {
		return Node{}, false, mountdav.ErrWriteInvalid
	}
	if err := ctx.Err(); err != nil {
		return Node{}, false, err
	}
	value = normalizedPath(value)
	if err := validateAbsolutePath(value); err != nil {
		return Node{}, false, err
	}
	current := Node{ObjectID: projection.RootParent, Kind: mountfs.KindDirectory}
	if value == "/" {
		return current, true, nil
	}
	for _, component := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if current.Kind != mountfs.KindDirectory {
			return Node{}, false, nil
		}
		dirent, found, err := projection.LiveDirentByName(resolver.db, resolver.driveID, current.ObjectID, component)
		if err != nil || !found {
			return Node{}, found, err
		}
		current, err = resolver.nodeFromDirent(ctx, dirent)
		if err != nil {
			return Node{}, false, err
		}
	}
	return current, true, ctx.Err()
}

func (resolver projectionResolver) nodeFromDirent(ctx context.Context, dirent projection.Dirent) (Node, error) {
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	switch dirent.ObjectKind {
	case projection.ObjectKindFolder:
		return Node{
			ObjectID: dirent.ObjectID,
			ParentID: dirent.ParentID,
			Name:     dirent.DisplayName,
			Kind:     mountfs.KindDirectory,
			Revision: uint64(dirent.Revision),
		}, nil
	case projection.ObjectKindFile:
		msgID, err := strconv.ParseInt(strings.TrimPrefix(dirent.ObjectID, projection.FileIDPrefix), 10, 64)
		if err != nil || msgID <= 0 {
			return Node{}, mountdav.ErrWriteUnavailable
		}
		file, found, err := projection.FileByID(resolver.db, resolver.driveID, msgID)
		if err != nil || !found {
			return Node{}, err
		}
		return Node{
			ObjectID:    dirent.ObjectID,
			ParentID:    dirent.ParentID,
			Name:        dirent.DisplayName,
			Kind:        mountfs.KindFile,
			Revision:    uint64(file.Revision),
			Size:        file.Size,
			ContentHash: file.ContentHash,
			Encrypted:   file.Encrypted,
		}, nil
	default:
		return Node{}, mountdav.ErrWriteUnavailable
	}
}

var _ Resolver = projectionResolver{}
