package mountcontroller

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"TDrive/backend/mountdav"
	"TDrive/backend/mountfs"
)

type rootedWriteDelegate interface {
	WriteSession
	mountdav.WriteCoordinator
}

// rootedWriteSession exposes one writable child below an immutable aggregate
// root. Every other root, including the virtual mount root, remains read-only.
type rootedWriteSession struct {
	root     string
	delegate rootedWriteDelegate
}

func newRootedWriteSession(root string, delegate rootedWriteDelegate) (*rootedWriteSession, error) {
	normalized, err := mountfs.NormalizeWritableName(root)
	if err != nil || normalized != root || delegate == nil {
		return nil, fmt.Errorf("%w: aggregate writer root is invalid", ErrInvalidConfiguration)
	}
	return &rootedWriteSession{root: root, delegate: delegate}, nil
}

func (writer *rootedWriteSession) Put(
	ctx context.Context,
	request mountdav.PutRequest,
	body io.Reader,
) (mountdav.MutationResult, error) {
	path, err := writer.childPath(request.Path)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	conditions, err := writer.childConditions(request.Conditions)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	next := request
	next.Path = path
	next.Conditions = conditions
	return writer.delegate.Put(ctx, next, body)
}

func (writer *rootedWriteSession) Mkdir(
	ctx context.Context,
	request mountdav.MkdirRequest,
) (mountdav.MutationResult, error) {
	path, err := writer.childPath(request.Path)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	conditions, err := writer.childConditions(request.Conditions)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	next := request
	next.Path = path
	next.Conditions = conditions
	return writer.delegate.Mkdir(ctx, next)
}

func (writer *rootedWriteSession) Move(
	ctx context.Context,
	request mountdav.MoveRequest,
) (mountdav.MutationResult, error) {
	source, err := writer.childPath(request.SourcePath)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	destination, err := writer.childPath(request.DestinationPath)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	conditions, err := writer.childConditions(request.Conditions)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	next := request
	next.SourcePath = source
	next.DestinationPath = destination
	next.Conditions = conditions
	return writer.delegate.Move(ctx, next)
}

func (writer *rootedWriteSession) Delete(
	ctx context.Context,
	request mountdav.DeleteRequest,
) (mountdav.MutationResult, error) {
	path, err := writer.childPath(request.Path)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	conditions, err := writer.childConditions(request.Conditions)
	if err != nil {
		return mountdav.MutationResult{}, err
	}
	next := request
	next.Path = path
	next.Conditions = conditions
	return writer.delegate.Delete(ctx, next)
}

func (writer *rootedWriteSession) WriteStatus() WriteStatus {
	if writer == nil || writer.delegate == nil {
		return WriteStatus{}
	}
	return writer.delegate.WriteStatus()
}

func (writer *rootedWriteSession) Drain(ctx context.Context) error {
	if writer == nil || writer.delegate == nil {
		return ErrInvalidConfiguration
	}
	return writer.delegate.Drain(ctx)
}

func (writer *rootedWriteSession) Close(ctx context.Context) error {
	if writer == nil || writer.delegate == nil {
		return ErrInvalidConfiguration
	}
	return writer.delegate.Close(ctx)
}

func (writer *rootedWriteSession) childPath(path string) (string, error) {
	if writer == nil || writer.root == "" || !strings.HasPrefix(path, "/") {
		return "", mountdav.ErrWriteInvalid
	}
	first, rest, found := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if mountfs.NameKey(first) != mountfs.NameKey(writer.root) || !found || rest == "" {
		slog.Warn("mountcontroller: aggregate write rejected, path escapes its rooted drive", "root", writer.root)
		return "", mountdav.ErrWriteForbidden
	}
	return "/" + rest, nil
}

func (writer *rootedWriteSession) childConditions(
	conditions mountdav.MutationConditions,
) (mountdav.MutationConditions, error) {
	next := mountdav.MutationConditions{
		IfMatch: mountdav.ETagConditions{
			Present: conditions.IfMatch.Present,
			Any:     conditions.IfMatch.Any,
			Tags:    append([]mountdav.EntityTag(nil), conditions.IfMatch.Tags...),
		},
		IfNoneMatch: mountdav.ETagConditions{
			Present: conditions.IfNoneMatch.Present,
			Any:     conditions.IfNoneMatch.Any,
			Tags:    append([]mountdav.EntityTag(nil), conditions.IfNoneMatch.Tags...),
		},
		LockTokens: append([]string(nil), conditions.LockTokens...),
		DAVIf:      make([]mountdav.DAVConditionList, len(conditions.DAVIf)),
	}
	for index, list := range conditions.DAVIf {
		path, err := writer.childPath(list.ResourcePath)
		if err != nil {
			return mountdav.MutationConditions{}, err
		}
		next.DAVIf[index] = mountdav.DAVConditionList{
			ResourcePath: path,
			Conditions:   append([]mountdav.DAVCondition(nil), list.Conditions...),
		}
	}
	return next, nil
}

var _ mountdav.WriteCoordinator = (*rootedWriteSession)(nil)
var _ WriteSession = (*rootedWriteSession)(nil)
