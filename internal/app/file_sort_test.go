package app

import (
	"testing"

	"TDrive/backend/datadir"
)

func TestFileSortPreferencePersistsAcrossAppInstances(t *testing.T) {
	datadir.Set(t.TempDir())
	t.Cleanup(func() { datadir.Set("") })

	app := &App{}
	if got, err := app.GetFileSortPreference(); err != nil || got != nil {
		t.Fatalf("initial preference=%v error=%v, want no preference", got, err)
	}
	first := FileSortPreference{Key: "name", Direction: "asc"}
	if err := app.SetFileSortPreference(first); err != nil {
		t.Fatal(err)
	}
	want := FileSortPreference{Key: "size", Direction: "desc"}
	if err := app.SetFileSortPreference(want); err != nil {
		t.Fatal(err)
	}
	if err := app.SetFileSortPreference(FileSortPreference{Key: "unknown", Direction: "desc"}); err == nil {
		t.Fatal("accepted an invalid sort key")
	}
	got, err := (&App{}).GetFileSortPreference()
	if err != nil || got == nil || *got != want {
		t.Fatalf("restored preference=%v error=%v, want %+v", got, err, want)
	}
}
