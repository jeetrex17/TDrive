package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchProjectMakesGeneratedProjectArchiveReady(t *testing.T) {
	input := generatedProjectFixture()

	got, err := patchProject(input, "16.0", "1.9.0", "42")
	if err != nil {
		t.Fatalf("patchProject: %v", err)
	}

	wants := []string{
		"Assets.xcassets in Resources",
		"LaunchScreen.storyboard in Resources",
		"PBXResourcesBuildPhase",
		"CLANG_ENABLE_OBJC_ARC = YES;",
		"CODE_SIGNING_ALLOWED = YES;",
		"CODE_SIGN_STYLE = Automatic;",
		"IPHONEOS_DEPLOYMENT_TARGET = 16.0;",
		"MARKETING_VERSION = 1.9.0;",
		"CURRENT_PROJECT_VERSION = 42;",
		"TARGETED_DEVICE_FAMILY = \"1,2\";",
		`/bin/sh \"${PROJECT_DIR}/../scripts/build_xcode_archive.sh\"`,
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("patched project does not contain %q", want)
		}
	}

	if strings.Contains(got, "CODE_SIGNING_ALLOWED = NO;") {
		t.Error("patched project still disables code signing")
	}
	if count := strings.Count(got, "IPHONEOS_DEPLOYMENT_TARGET = 16.0;"); count != 2 {
		t.Errorf("deployment target count = %d, want 2", count)
	}
}

func TestPatchProjectRejectsTemplateDrift(t *testing.T) {
	_, err := patchProject(strings.Replace(generatedProjectFixture(), "CODE_SIGNING_ALLOWED = NO;", "", 1), "16.0", "1.9.0", "42")
	if err == nil {
		t.Fatal("patchProject succeeded after generated template drift")
	}
	if !strings.Contains(err.Error(), "disabled signing") {
		t.Fatalf("error = %q, want disabled signing context", err)
	}
}

func TestRunPatchesProjectFileAtomically(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project.pbxproj")
	if err := os.WriteFile(projectPath, []byte(generatedProjectFixture()), 0o640); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := run(projectPath, "16.0", "1.9.0", "42"); err != nil {
		t.Fatalf("run: %v", err)
	}
	contents, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatalf("read patched project: %v", err)
	}
	if !strings.Contains(string(contents), "PBXResourcesBuildPhase") {
		t.Error("patched file does not contain resources phase")
	}
	info, err := os.Stat(projectPath)
	if err != nil {
		t.Fatalf("stat patched project: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("patched file mode = %o, want 640", got)
	}
}

func TestRunValidatesInputs(t *testing.T) {
	tests := []struct {
		name             string
		path             string
		minimumVersion   string
		marketingVersion string
		buildNumber      string
		want             string
	}{
		{name: "project name", path: "not-a-project", minimumVersion: "16.0", marketingVersion: "1.9.0", buildNumber: "42", want: "project.pbxproj"},
		{name: "minimum version", path: "project.pbxproj", minimumVersion: "latest", marketingVersion: "1.9.0", buildNumber: "42", want: "minimum iOS version"},
		{name: "marketing version", path: "project.pbxproj", minimumVersion: "16.0", marketingVersion: "beta", buildNumber: "42", want: "marketing version"},
		{name: "build number", path: "project.pbxproj", minimumVersion: "16.0", marketingVersion: "1.9.0", buildNumber: "latest", want: "build number"},
		{name: "missing project", path: filepath.Join(t.TempDir(), "project.pbxproj"), minimumVersion: "16.0", marketingVersion: "1.9.0", buildNumber: "42", want: "stat project"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.path, test.minimumVersion, test.marketingVersion, test.buildNumber)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestRunReportsReadAndTemplateErrors(t *testing.T) {
	projectDirectory := filepath.Join(t.TempDir(), "project.pbxproj")
	if err := os.Mkdir(projectDirectory, 0o755); err != nil {
		t.Fatalf("create project directory: %v", err)
	}
	if err := run(projectDirectory, "16.0", "1.9.0", "42"); err == nil || !strings.Contains(err.Error(), "read project") {
		t.Fatalf("directory read error = %v", err)
	}

	projectPath := filepath.Join(t.TempDir(), "project.pbxproj")
	drifted := strings.Replace(generatedProjectFixture(), "CODE_SIGNING_ALLOWED = NO;", "", 1)
	if err := os.WriteFile(projectPath, []byte(drifted), 0o644); err != nil {
		t.Fatalf("write drifted project: %v", err)
	}
	if err := run(projectPath, "16.0", "1.9.0", "42"); err == nil || !strings.Contains(err.Error(), "disabled signing") {
		t.Fatalf("template drift error = %v", err)
	}
}

func TestReplaceFileAtomicallyReportsFilesystemErrors(t *testing.T) {
	missingParent := filepath.Join(t.TempDir(), "missing", "project.pbxproj")
	if err := replaceFileAtomically(missingParent, []byte("project"), 0o644); err == nil || !strings.Contains(err.Error(), "temporary project") {
		t.Fatalf("missing parent error = %v", err)
	}

	destinationDirectory := filepath.Join(t.TempDir(), "project.pbxproj")
	if err := os.Mkdir(destinationDirectory, 0o755); err != nil {
		t.Fatalf("create destination directory: %v", err)
	}
	if err := replaceFileAtomically(destinationDirectory, []byte("project"), 0o644); err == nil || !strings.Contains(err.Error(), "replace project") {
		t.Fatalf("directory destination error = %v", err)
	}
}

func TestPatchProjectRejectsReservedIDsAndMalformedShellScript(t *testing.T) {
	withReservedID := generatedProjectFixture() + assetsBuildID
	if _, err := patchProject(withReservedID, "16.0", "1.9.0", "42"); err == nil || !strings.Contains(err.Error(), "reserved object ID") {
		t.Fatalf("reserved ID error = %v", err)
	}

	malformed := "\t\t\tshellScript = \"unterminated\n"
	if _, err := replaceShellScript(malformed); err == nil || !strings.Contains(err.Error(), "terminator") {
		t.Fatalf("malformed shell script error = %v", err)
	}
	if _, err := replaceShellScript("no shell script"); err == nil || !strings.Contains(err.Error(), "match count") {
		t.Fatalf("missing shell script error = %v", err)
	}
}

func generatedProjectFixture() string {
	return `/* Begin PBXBuildFile section */
		C0DEBEEF0000000000000001 /* main.m in Sources */ = {isa = PBXBuildFile; fileRef = C0DEBEEF0000000000000002 /* main.m */; };
/* End PBXBuildFile section */
/* Begin PBXFileReference section */
		C0DEBEEF0000000000000003 /* Info.plist */ = {isa = PBXFileReference; lastKnownFileType = text.plist.xml; path = Info.plist; sourceTree = "<group>"; };
/* End PBXFileReference section */
		C0DEBEEF0000000000000030 /* main */ = {
			isa = PBXGroup;
			children = (
				C0DEBEEF0000000000000002 /* main.m */,
				C0DEBEEF0000000000000003 /* Info.plist */,
			);
			path = main;
			sourceTree = SOURCE_ROOT;
		};
			buildPhases = (
				C0DEBEEF0000000000000055 /* Prebuild: Wails Go Archive */,
				C0DEBEEF0000000000000050 /* Sources */,
				C0DEBEEF0000000000000056 /* Frameworks */,
			);
/* Begin PBXShellScriptBuildPhase section */
		C0DEBEEF0000000000000055 /* Prebuild: Wails Go Archive */ = {
			isa = PBXShellScriptBuildPhase;
			shellPath = /bin/sh;
			shellScript = "generated script";
		};
/* End PBXShellScriptBuildPhase section */
		C0DEBEEF0000000000000090 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				IPHONEOS_DEPLOYMENT_TARGET = 15.0;
				CODE_SIGNING_ALLOWED = NO;
			};
		};
		C0DEBEEF00000000000000A0 /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				IPHONEOS_DEPLOYMENT_TARGET = 15.0;
				CODE_SIGNING_ALLOWED = NO;
			};
		};
`
}
