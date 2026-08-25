package mountfs

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// AggregateRoot describes one drive exposed below an Aggregate's virtual root.
// Name is the requested top-level directory name. Personal roots are always
// exported from the canonical source name "Personal".
type AggregateRoot struct {
	DriveID  int64
	Name     string
	Personal bool
	FS       *FS
}

type aggregateRoot struct {
	driveID  int64
	personal bool
	entry    Entry
	fs       *FS
}

// Aggregate exposes multiple independently-backed filesystems beneath one
// virtual root. It is immutable after construction and safe for concurrent
// reads.
type Aggregate struct {
	roots       []aggregateRoot
	rootByName  map[string]int
	nameByDrive map[int64]string
}

// NewAggregate creates a filesystem over an explicit, non-empty drive
// selection. Construction only validates and indexes the supplied children; it
// never reads their directory sources.
func NewAggregate(input []AggregateRoot) (*Aggregate, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("%w: no drives selected", ErrInvalidConfiguration)
	}

	roots, err := prepareAggregateRoots(input)
	if err != nil {
		return nil, err
	}
	sortAggregateRoots(roots)

	rootByName := make(map[string]int, len(roots))
	nameByDrive := make(map[int64]string, len(roots))
	for index := range roots {
		rootByName[NameKey(roots[index].entry.Name)] = index
		nameByDrive[roots[index].driveID] = roots[index].entry.Name
	}
	return &Aggregate{
		roots:       roots,
		rootByName:  rootByName,
		nameByDrive: nameByDrive,
	}, nil
}

// RootName returns the immutable exported top-level name for a drive.
func (aggregate *Aggregate) RootName(driveID int64) (string, bool) {
	if aggregate == nil {
		return "", false
	}
	name, ok := aggregate.nameByDrive[driveID]
	return name, ok
}

// ReadDir lists the virtual drive roots or delegates to the selected child.
func (aggregate *Aggregate) ReadDir(ctx context.Context, path string) ([]Entry, error) {
	parts, err := aggregate.validatedParts(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		entries := make([]Entry, len(aggregate.roots))
		for index := range aggregate.roots {
			entries[index] = aggregate.roots[index].entry
		}
		return entries, nil
	}

	root, childPath, err := aggregate.route(parts)
	if err != nil {
		return nil, err
	}
	return root.fs.ReadDir(ctx, childPath)
}

// Stat returns virtual-root metadata or delegates to the selected child.
func (aggregate *Aggregate) Stat(ctx context.Context, path string) (Entry, error) {
	parts, err := aggregate.validatedParts(ctx, path)
	if err != nil {
		return Entry{}, err
	}
	if len(parts) == 0 {
		return Entry{ID: RootID, ParentID: RootID, Kind: KindDirectory}, nil
	}

	root, childPath, err := aggregate.route(parts)
	if err != nil {
		return Entry{}, err
	}
	if len(parts) == 1 {
		return root.entry, nil
	}
	return root.fs.Stat(ctx, childPath)
}

// Open delegates file access to the selected child. The aggregate root and
// drive roots are directories and cannot be opened as files.
func (aggregate *Aggregate) Open(ctx context.Context, path string) (*File, error) {
	parts, err := aggregate.validatedParts(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, ErrIsDirectory
	}

	root, childPath, err := aggregate.route(parts)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 {
		return nil, ErrIsDirectory
	}
	return root.fs.Open(ctx, childPath)
}

func prepareAggregateRoots(input []AggregateRoot) ([]aggregateRoot, error) {
	roots := make([]aggregateRoot, len(input))
	entries := make([]snapshotEntry, len(input))
	bases := make([]string, len(input))
	baseGroups := make(map[string][]int, len(input))
	seenDrives := make(map[int64]struct{}, len(input))

	for index, candidate := range slices.Clone(input) {
		if err := validateAggregateRoot(candidate, seenDrives); err != nil {
			return nil, err
		}
		seenDrives[candidate.DriveID] = struct{}{}

		sourceName := candidate.Name
		if candidate.Personal {
			sourceName = "Personal"
		}
		base := portableName(sourceName)
		bases[index] = base
		baseGroups[NameKey(base)] = append(baseGroups[NameKey(base)], index)
		entries[index] = snapshotEntry{
			source: SourceEntry{
				ID:       strconv.FormatInt(candidate.DriveID, 10),
				ParentID: RootID,
				Name:     sourceName,
				Kind:     KindDirectory,
			},
			entry: Entry{
				ChannelID:  candidate.DriveID,
				ID:         aggregateRootID(candidate.DriveID),
				ParentID:   RootID,
				SourceName: sourceName,
				Kind:       KindDirectory,
			},
		}
		roots[index] = aggregateRoot{
			driveID:  candidate.DriveID,
			personal: candidate.Personal,
			fs:       candidate.FS,
		}
	}

	if err := assignUniqueNames(entries, bases, baseGroups); err != nil {
		return nil, fmt.Errorf("%w: aggregate root aliases: %v", ErrInvalidConfiguration, err)
	}
	for index := range roots {
		roots[index].entry = entries[index].entry
	}
	return roots, nil
}

func validateAggregateRoot(candidate AggregateRoot, seenDrives map[int64]struct{}) error {
	if candidate.DriveID <= 0 || candidate.FS == nil {
		return fmt.Errorf("%w: invalid aggregate drive", ErrInvalidConfiguration)
	}
	if candidate.FS.channelID != candidate.DriveID || candidate.FS.source == nil || candidate.FS.opener == nil {
		return fmt.Errorf("%w: drive %d filesystem mismatch", ErrInvalidConfiguration, candidate.DriveID)
	}
	if _, exists := seenDrives[candidate.DriveID]; exists {
		return fmt.Errorf("%w: duplicate drive %d", ErrInvalidConfiguration, candidate.DriveID)
	}
	if !candidate.Personal && strings.TrimSpace(candidate.Name) == "" {
		return fmt.Errorf("%w: drive %d has no name", ErrInvalidConfiguration, candidate.DriveID)
	}
	return nil
}

func sortAggregateRoots(roots []aggregateRoot) {
	sort.Slice(roots, func(left, right int) bool {
		if roots[left].personal != roots[right].personal {
			return roots[left].personal
		}
		leftKey := NameKey(roots[left].entry.Name)
		rightKey := NameKey(roots[right].entry.Name)
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		if roots[left].entry.Name != roots[right].entry.Name {
			return roots[left].entry.Name < roots[right].entry.Name
		}
		return roots[left].driveID < roots[right].driveID
	})
}

func (aggregate *Aggregate) validatedParts(ctx context.Context, path string) ([]string, error) {
	if aggregate == nil || len(aggregate.roots) == 0 || len(aggregate.rootByName) != len(aggregate.roots) {
		return nil, ErrInvalidConfiguration
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidConfiguration)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return splitAbsolutePath(path)
}

func (aggregate *Aggregate) route(parts []string) (aggregateRoot, string, error) {
	index, exists := aggregate.rootByName[NameKey(parts[0])]
	if !exists {
		return aggregateRoot{}, "", ErrNotFound
	}
	root := aggregate.roots[index]
	if len(parts) == 1 {
		return root, "/", nil
	}
	return root, "/" + strings.Join(parts[1:], "/"), nil
}

func aggregateRootID(driveID int64) string {
	return "drive:" + strconv.FormatInt(driveID, 10)
}

var _ ReadFilesystem = (*Aggregate)(nil)
