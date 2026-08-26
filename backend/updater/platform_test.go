package updater

import "testing"

func TestAppAssetName(t *testing.T) {
	cases := []struct {
		platform Platform
		want     string
		ok       bool
	}{
		{Platform{"darwin", "arm64"}, "TDrive-v1.7.0-macos-arm64.zip", true},
		{Platform{"windows", "amd64"}, "TDrive-v1.7.0-windows-amd64.zip", true},
		{Platform{"linux", "amd64"}, "TDrive-v1.7.0-linux-amd64.AppImage", true},
		{Platform{"darwin", "amd64"}, "", false},
		{Platform{"linux", "arm64"}, "", false},
	}
	for _, tc := range cases {
		got, ok := appAssetName("v1.7.0", tc.platform)
		if ok != tc.ok || got != tc.want {
			t.Errorf("appAssetName(%v) = %q, %v; want %q, %v", tc.platform, got, ok, tc.want, tc.ok)
		}
	}
}

func TestReleaseAssetMatchesExactNameOnly(t *testing.T) {
	release := Release{Assets: []Asset{
		{Name: "TDrive-v1.7.0-windows-amd64-cli.zip"},
		{Name: "TDrive-v1.7.0-windows-amd64.zip", Size: 7},
	}}
	asset, ok := release.asset("TDrive-v1.7.0-windows-amd64.zip")
	if !ok || asset.Size != 7 {
		t.Fatalf("asset lookup = %+v, %v; want the desktop zip", asset, ok)
	}
	if _, ok := release.asset("TDrive-v1.7.0-macos-arm64.zip"); ok {
		t.Fatalf("missing asset must not match")
	}
}
