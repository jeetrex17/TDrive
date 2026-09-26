package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	assetsBuildID     = "C0DEBEEF0000000000000005"
	launchBuildID     = "C0DEBEEF0000000000000006"
	assetsReferenceID = "C0DEBEEF0000000000000007"
	launchReferenceID = "C0DEBEEF0000000000000008"
	resourcesPhaseID  = "C0DEBEEF0000000000000057"
	projectScriptPath = "/bin/sh \\\"${PROJECT_DIR}/../scripts/build_xcode_archive.sh\\\""
)

type replacement struct {
	name  string
	old   string
	new   string
	count int
}

func main() {
	projectPath := flag.String("project", "", "path to the generated project.pbxproj")
	minimumVersion := flag.String("minimum-version", "", "minimum supported iOS version")
	marketingVersion := flag.String("marketing-version", "", "app marketing version")
	buildNumber := flag.String("build-number", "", "unique App Store build number")
	flag.Parse()

	if err := run(*projectPath, *minimumVersion, *marketingVersion, *buildNumber); err != nil {
		fmt.Fprintf(os.Stderr, "patch generated Xcode project: %v\n", err)
		os.Exit(1)
	}
}

func run(projectPath, minimumVersion, marketingVersion, buildNumber string) error {
	if filepath.Base(projectPath) != "project.pbxproj" {
		return errors.New("-project must point to project.pbxproj")
	}
	versionPattern := regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){1,2}$`)
	if !versionPattern.MatchString(minimumVersion) {
		return fmt.Errorf("invalid minimum iOS version %q", minimumVersion)
	}
	if !versionPattern.MatchString(marketingVersion) {
		return fmt.Errorf("invalid marketing version %q", marketingVersion)
	}
	if !versionPattern.MatchString(buildNumber) && !regexp.MustCompile(`^[0-9]+$`).MatchString(buildNumber) {
		return fmt.Errorf("invalid build number %q", buildNumber)
	}

	info, err := os.Stat(projectPath)
	if err != nil {
		return fmt.Errorf("stat project: %w", err)
	}
	contents, err := os.ReadFile(projectPath)
	if err != nil {
		return fmt.Errorf("read project: %w", err)
	}
	patched, err := patchProject(string(contents), minimumVersion, marketingVersion, buildNumber)
	if err != nil {
		return err
	}
	return replaceFileAtomically(projectPath, []byte(patched), info.Mode())
}

func patchProject(project, minimumVersion, marketingVersion, buildNumber string) (string, error) {
	for _, id := range []string{assetsBuildID, launchBuildID, assetsReferenceID, launchReferenceID, resourcesPhaseID} {
		if strings.Contains(project, id) {
			return "", fmt.Errorf("generated project unexpectedly already contains reserved object ID %s", id)
		}
	}

	replacements := []replacement{
		{
			name: "resource build files",
			old:  "/* End PBXBuildFile section */",
			new: "\t\t" + assetsBuildID + " /* Assets.xcassets in Resources */ = {isa = PBXBuildFile; fileRef = " + assetsReferenceID + " /* Assets.xcassets */; };\n" +
				"\t\t" + launchBuildID + " /* LaunchScreen.storyboard in Resources */ = {isa = PBXBuildFile; fileRef = " + launchReferenceID + " /* LaunchScreen.storyboard */; };\n" +
				"/* End PBXBuildFile section */",
			count: 1,
		},
		{
			name: "resource file references",
			old:  "/* End PBXFileReference section */",
			new: "\t\t" + assetsReferenceID + " /* Assets.xcassets */ = {isa = PBXFileReference; lastKnownFileType = folder.assetcatalog; path = Assets.xcassets; sourceTree = \"<group>\"; };\n" +
				"\t\t" + launchReferenceID + " /* LaunchScreen.storyboard */ = {isa = PBXFileReference; lastKnownFileType = file.storyboard; path = LaunchScreen.storyboard; sourceTree = \"<group>\"; };\n" +
				"/* End PBXFileReference section */",
			count: 1,
		},
		{
			name:  "main resource group",
			old:   "\t\t\t\tC0DEBEEF0000000000000003 /* Info.plist */,\n",
			new:   "\t\t\t\tC0DEBEEF0000000000000003 /* Info.plist */,\n\t\t\t\t" + assetsReferenceID + " /* Assets.xcassets */,\n\t\t\t\t" + launchReferenceID + " /* LaunchScreen.storyboard */,\n",
			count: 1,
		},
		{
			name:  "target resources phase",
			old:   "\t\t\t\tC0DEBEEF0000000000000050 /* Sources */,\n",
			new:   "\t\t\t\tC0DEBEEF0000000000000050 /* Sources */,\n\t\t\t\t" + resourcesPhaseID + " /* Resources */,\n",
			count: 1,
		},
		{
			name: "resources phase",
			old:  "/* Begin PBXShellScriptBuildPhase section */",
			new: "/* Begin PBXResourcesBuildPhase section */\n" +
				"\t\t" + resourcesPhaseID + " /* Resources */ = {\n" +
				"\t\t\tisa = PBXResourcesBuildPhase;\n" +
				"\t\t\tbuildActionMask = 2147483647;\n" +
				"\t\t\tfiles = (\n" +
				"\t\t\t\t" + assetsBuildID + " /* Assets.xcassets in Resources */,\n" +
				"\t\t\t\t" + launchBuildID + " /* LaunchScreen.storyboard in Resources */,\n" +
				"\t\t\t);\n" +
				"\t\t\trunOnlyForDeploymentPostprocessing = 0;\n" +
				"\t\t};\n" +
				"/* End PBXResourcesBuildPhase section */\n\n" +
				"/* Begin PBXShellScriptBuildPhase section */",
			count: 1,
		},
		{
			name:  "deployment target",
			old:   "\t\t\t\tIPHONEOS_DEPLOYMENT_TARGET = 15.0;\n",
			new:   "\t\t\t\tIPHONEOS_DEPLOYMENT_TARGET = " + minimumVersion + ";\n",
			count: 2,
		},
		{
			name: "disabled signing",
			old:  "\t\t\t\tCODE_SIGNING_ALLOWED = NO;\n",
			new: "\t\t\t\tASSETCATALOG_COMPILER_APPICON_NAME = AppIcon;\n" +
				"\t\t\t\tCLANG_ENABLE_OBJC_ARC = YES;\n" +
				"\t\t\t\tCODE_SIGNING_ALLOWED = YES;\n" +
				"\t\t\t\tCODE_SIGN_STYLE = Automatic;\n" +
				"\t\t\t\tCURRENT_PROJECT_VERSION = " + buildNumber + ";\n" +
				"\t\t\t\tMARKETING_VERSION = " + marketingVersion + ";\n" +
				"\t\t\t\tTARGETED_DEVICE_FAMILY = \"1,2\";\n",
			count: 2,
		},
	}

	var err error
	for _, item := range replacements {
		project, err = replaceExactly(project, item)
		if err != nil {
			return "", err
		}
	}
	return replaceShellScript(project)
}

func replaceExactly(contents string, item replacement) (string, error) {
	if count := strings.Count(contents, item.old); count != item.count {
		return "", fmt.Errorf("%s template match count = %d, want %d", item.name, count, item.count)
	}
	return strings.ReplaceAll(contents, item.old, item.new), nil
}

func replaceShellScript(project string) (string, error) {
	const prefix = "\t\t\tshellScript = \""
	if count := strings.Count(project, prefix); count != 1 {
		return "", fmt.Errorf("Go archive shell script match count = %d, want 1", count)
	}
	start := strings.Index(project, prefix)
	relativeEnd := strings.Index(project[start+len(prefix):], "\";\n")
	if relativeEnd < 0 {
		return "", errors.New("Go archive shell script terminator not found")
	}
	end := start + len(prefix) + relativeEnd + len("\";\n")
	replacement := "\t\t\tshellScript = \"" + projectScriptPath + "\";\n"
	return project[:start] + replacement + project[end:], nil
}

func replaceFileAtomically(path string, contents []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".project.pbxproj-*")
	if err != nil {
		return fmt.Errorf("create temporary project: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary project mode: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary project: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary project: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace project: %w", err)
	}
	return nil
}
