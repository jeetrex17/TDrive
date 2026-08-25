package mountfs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestNewAggregateRejectsInvalidRoots(t *testing.T) {
	t.Parallel()

	validFS := mustNewFS(t, 42, newFakeDirectorySource(nil), &fakeContentOpener{})
	otherFS := mustNewFS(t, 99, newFakeDirectorySource(nil), &fakeContentOpener{})
	tests := []struct {
		name  string
		roots []AggregateRoot
	}{
		{name: "empty selection"},
		{name: "zero drive ID", roots: []AggregateRoot{{Name: "Personal", FS: validFS}}},
		{name: "nil filesystem", roots: []AggregateRoot{{DriveID: 42, Name: "Personal"}}},
		{name: "filesystem drive mismatch", roots: []AggregateRoot{{DriveID: 42, Name: "Personal", FS: otherFS}}},
		{name: "duplicate drive", roots: []AggregateRoot{
			{DriveID: 42, Name: "Personal", FS: validFS},
			{DriveID: 42, Name: "Shared", FS: validFS},
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewAggregate(test.roots); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("NewAggregate() error = %v, want ErrInvalidConfiguration", err)
			}
		})
	}
}

func TestAggregateRootIsPortableDeterministicAndPersonalFirst(t *testing.T) {
	t.Parallel()

	personalFS := mustNewFS(t, 30, newFakeDirectorySource(nil), &fakeContentOpener{})
	sharedFirstFS := mustNewFS(t, 10, newFakeDirectorySource(nil), &fakeContentOpener{})
	sharedSecondFS := mustNewFS(t, 20, newFakeDirectorySource(nil), &fakeContentOpener{})
	forward := []AggregateRoot{
		{DriveID: 20, Name: `Shared: Team.`, FS: sharedSecondFS},
		{DriveID: 30, Name: "Personal", Personal: true, FS: personalFS},
		{DriveID: 10, Name: `shared_ team_`, FS: sharedFirstFS},
	}
	reversed := slices.Clone(forward)
	slices.Reverse(reversed)

	first := mustNewAggregate(t, forward)
	second := mustNewAggregate(t, reversed)
	firstEntries, err := first.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir(first root) error = %v", err)
	}
	secondEntries, err := second.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir(second root) error = %v", err)
	}
	if !slices.Equal(firstEntries, secondEntries) {
		t.Fatalf("aggregate aliases depend on input order:\nfirst:  %#v\nsecond: %#v", firstEntries, secondEntries)
	}
	if len(firstEntries) != 3 || firstEntries[0].ChannelID != 30 || firstEntries[0].Name != "Personal" {
		t.Fatalf("root order = %#v, want Personal first", firstEntries)
	}
	for _, entry := range firstEntries[1:] {
		if entry.Kind != KindDirectory || entry.SourceName == "" || !strings.Contains(entry.Name, "[td-d-") {
			t.Errorf("colliding shared root was not exported safely: %#v", entry)
		}
	}
	if NameKey(firstEntries[1].Name) == NameKey(firstEntries[2].Name) {
		t.Fatalf("root names collide after export: %#v", firstEntries)
	}

	for _, driveID := range []int64{10, 20, 30} {
		firstName, firstOK := first.RootName(driveID)
		secondName, secondOK := second.RootName(driveID)
		if !firstOK || !secondOK || firstName != secondName {
			t.Errorf("RootName(%d) = (%q, %t) and (%q, %t), want equal", driveID, firstName, firstOK, secondName, secondOK)
		}
	}
	if _, ok := first.RootName(999); ok {
		t.Fatal("RootName(unknown) reported a match")
	}
}

func TestAggregateRoutesOperationsWithoutEagerChildReads(t *testing.T) {
	t.Parallel()

	personalSource := newFakeDirectorySource(map[string][]SourceEntry{
		RootID: {
			{ID: "d:docs", ParentID: RootID, Name: "Docs", Kind: KindDirectory},
		},
		"d:docs": {
			{ID: "f:note", ParentID: "d:docs", Name: "Note.txt", Kind: KindFile, Size: 4},
		},
	})
	personalContent := &memoryContent{data: []byte("note")}
	personalFS := mustNewFS(t, 11, personalSource, &fakeContentOpener{content: personalContent})
	sharedSource := newFakeDirectorySource(nil)
	sharedFS := mustNewFS(t, 22, sharedSource, &fakeContentOpener{})
	roots := []AggregateRoot{
		{DriveID: 11, Name: "Personal", Personal: true, FS: personalFS},
		{DriveID: 22, Name: "Shared — Work", FS: sharedFS},
	}
	aggregate := mustNewAggregate(t, roots)

	if personalSource.callCount() != 0 || sharedSource.callCount() != 0 {
		t.Fatal("NewAggregate eagerly read a child filesystem")
	}
	root, err := aggregate.Stat(context.Background(), "/")
	if err != nil || root.Kind != KindDirectory || root.Name != "" || root.ChannelID != 0 {
		t.Fatalf("Stat(root) = (%#v, %v)", root, err)
	}
	personal, err := aggregate.Stat(context.Background(), "/Personal")
	if err != nil || personal.ChannelID != 11 || personal.Name != "Personal" || personal.Kind != KindDirectory {
		t.Fatalf("Stat(Personal) = (%#v, %v)", personal, err)
	}
	if personalSource.callCount() != 0 || sharedSource.callCount() != 0 {
		t.Fatal("virtual-root metadata eagerly read a child filesystem")
	}

	docs, err := aggregate.ReadDir(context.Background(), "/Personal")
	if err != nil || len(docs) != 1 || docs[0].ID != "d:docs" {
		t.Fatalf("ReadDir(Personal) = (%#v, %v)", docs, err)
	}
	note, err := aggregate.Stat(context.Background(), "/Personal/Docs/Note.txt")
	if err != nil || note.ChannelID != 11 || note.ID != "f:note" {
		t.Fatalf("Stat(nested) = (%#v, %v)", note, err)
	}
	file, err := aggregate.Open(context.Background(), "/Personal/Docs/Note.txt")
	if err != nil {
		t.Fatalf("Open(nested) error = %v", err)
	}
	defer file.Close()
	buffer := make([]byte, 4)
	if count, readErr := file.ReadAt(context.Background(), buffer, 0); readErr != nil || count != 4 || string(buffer) != "note" {
		t.Fatalf("ReadAt() = (%d, %v, %q)", count, readErr, buffer)
	}
	if sharedSource.callCount() != 0 {
		t.Fatalf("accessing Personal read Shared %d times", sharedSource.callCount())
	}
}

func TestAggregateReturnsTypedErrorsAndDefensiveRootCopies(t *testing.T) {
	t.Parallel()

	childFS := mustNewFS(t, 42, newFakeDirectorySource(nil), &fakeContentOpener{})
	input := []AggregateRoot{{DriveID: 42, Name: "Personal", Personal: true, FS: childFS}}
	aggregate := mustNewAggregate(t, input)
	input[0].Name = "Changed"
	input[0].FS = nil

	entries, err := aggregate.ReadDir(context.Background(), "/")
	if err != nil {
		t.Fatalf("ReadDir(root) error = %v", err)
	}
	entries[0].Name = "Mutated"
	again, err := aggregate.ReadDir(context.Background(), "/")
	if err != nil || len(again) != 1 || again[0].Name != "Personal" {
		t.Fatalf("second ReadDir(root) = (%#v, %v)", again, err)
	}

	if _, err := aggregate.Stat(context.Background(), "/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := aggregate.Open(context.Background(), "/"); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("Open(root) error = %v, want ErrIsDirectory", err)
	}
	if _, err := aggregate.Open(context.Background(), "/Personal"); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("Open(drive root) error = %v, want ErrIsDirectory", err)
	}
	if _, err := aggregate.ReadDir(context.Background(), "/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadDir(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := aggregate.Stat(nil, "/"); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Stat(nil context) error = %v, want ErrInvalidConfiguration", err)
	}
	if _, err := (*Aggregate)(nil).ReadDir(context.Background(), "/"); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil aggregate ReadDir() error = %v, want ErrInvalidConfiguration", err)
	}
}

func mustNewAggregate(t *testing.T, roots []AggregateRoot) *Aggregate {
	t.Helper()

	aggregate, err := NewAggregate(roots)
	if err != nil {
		t.Fatalf("NewAggregate() error = %v", err)
	}
	return aggregate
}
