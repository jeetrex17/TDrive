//go:build windows

package file

import "testing"

func TestWindowsExtendedPathSupportsDriveAndUNCPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "drive",
			path: `C:\Users\tester\Downloads\Project`,
			want: `\\?\C:\Users\tester\Downloads\Project`,
		},
		{
			name: "UNC",
			path: `\\server\share\Downloads\Project`,
			want: `\\?\UNC\server\share\Downloads\Project`,
		},
		{
			name: "already extended",
			path: `\\?\C:\very\long\Project`,
			want: `\\?\C:\very\long\Project`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := windowsExtendedPath(test.path); got != test.want {
				t.Fatalf("windowsExtendedPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}
