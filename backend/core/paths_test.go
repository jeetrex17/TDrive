package core

import (
	"testing"

	"TDrive/backend/projection"
)

func TestNormalizeRemotePath(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
		in   string
		want string
	}{
		{name: "empty input uses cwd", cwd: "/Photos", in: "", want: "/Photos"},
		{name: "relative path", cwd: "/Photos", in: "Trips", want: "/Photos/Trips"},
		{name: "absolute path", cwd: "/Photos", in: "/Docs", want: "/Docs"},
		{name: "dot dot cleans", cwd: "/Photos/Trips", in: "../Raw", want: "/Photos/Raw"},
		{name: "root stays root", cwd: "/", in: "..", want: "/"},
		{name: "relative cwd is normalized", cwd: "Photos", in: "Trips", want: "/Photos/Trips"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeRemotePath(tt.cwd, tt.in)
			if err != nil {
				t.Fatalf("NormalizeRemotePath: %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeRemotePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJoinRemotePath(t *testing.T) {
	tests := []struct {
		parent string
		name   string
		want   string
	}{
		{parent: "/", name: "a.txt", want: "/a.txt"},
		{parent: "/Docs", name: "a.txt", want: "/Docs/a.txt"},
		{parent: "", name: "Docs", want: "/Docs"},
		{parent: "/Docs", name: "", want: "/Docs"},
	}

	for _, tt := range tests {
		if got := JoinRemotePath(tt.parent, tt.name); got != tt.want {
			t.Fatalf("JoinRemotePath(%q, %q) = %q, want %q", tt.parent, tt.name, got, tt.want)
		}
	}
}

func TestSplitRemotePath(t *testing.T) {
	parent, name, err := SplitRemotePath("/Photos/Raw")
	if err != nil {
		t.Fatalf("SplitRemotePath: %v", err)
	}
	if parent != "/Photos" || name != "Raw" {
		t.Fatalf("SplitRemotePath() = (%q, %q), want (%q, %q)", parent, name, "/Photos", "Raw")
	}

	parent, name, err = SplitRemotePath("/Raw")
	if err != nil {
		t.Fatalf("SplitRemotePath root child: %v", err)
	}
	if parent != "/" || name != "Raw" {
		t.Fatalf("SplitRemotePath root child = (%q, %q), want (%q, %q)", parent, name, "/", "Raw")
	}

	if _, _, err := SplitRemotePath("/"); err == nil {
		t.Fatal("SplitRemotePath root succeeded, want error")
	}
}

func TestFolderPathFromMap(t *testing.T) {
	folders := map[string]projection.FolderSlim{
		"d:a": {ID: "d:a", Name: "A", ParentID: ""},
		"d:b": {ID: "d:b", Name: "B", ParentID: "d:a"},
	}
	got, err := folderPathFromMap(folders, "d:b")
	if err != nil {
		t.Fatalf("folderPathFromMap: %v", err)
	}
	if got != "/A/B" {
		t.Fatalf("folderPathFromMap() = %q, want %q", got, "/A/B")
	}

	if got, err := folderPathFromMap(folders, ""); err != nil || got != "/" {
		t.Fatalf("folderPathFromMap(root) = %q, %v; want /, nil", got, err)
	}

	if _, err := folderPathFromMap(folders, "d:missing"); err == nil {
		t.Fatal("folderPathFromMap missing folder succeeded, want error")
	}
}
