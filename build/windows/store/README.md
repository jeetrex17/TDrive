# Microsoft Store packaging

TDrive remains a native Wails desktop app. Its MSIX packages run with standard
user permissions (`runFullTrust`), not in an AppContainer. They include the same
mpv runtime as the Windows installer. No Telegram sessions, databases or user
files belong in a package.

## Store identity

These values come from Partner Center and must match exactly:

| Field | Value |
| --- | --- |
| Display name | TDrive: Telegram based Cloud Storage |
| Package name | Jeetrex.TDriveTelegrambasedCloudStorage |
| Publisher | CN=00B230FF-06D4-460F-89D0-2F55A6D78696 |
| Publisher display name | Jeetrex |
| Store ID | 9PK3XTTDC0C0 |

The package identity stays stable when branding changes. Update the display
name and reserve the corresponding name in Partner Center instead of creating
a replacement package identity.

## Build on Windows

Use the pinned Go and Wails versions in [the build guide](../../README.md).
Install the Windows SDK with `MakeAppx.exe` and `SignTool.exe`. The initial
package targets x64; native ARM64 packaging is not included.

1. Build the application with a release version, for example
   `wails3 task windows:build ARCH=amd64 VERSION=2.1.0`.
2. Prepare the qualified media runtime using
   `scripts/prepare-mpv-runtime.sh`, then run
   `./scripts/package-mpv-windows.ps1 build/bin/TDrive.exe` in PowerShell.
3. Package the existing executable and sibling `media` directory:
   `./scripts/package-msix.ps1 -AppExe build/bin/TDrive.exe -Version 2.1.0`.

Default output is an unsigned package for Store submission. Microsoft signs
the package after certification. The default packaging path requires a release
media runtime with recorded provenance and checksums. CI fixtures are not
approved release runtimes.

For laptop testing, use the same command with `-TestSign`. This produces a
test-signed package, its public certificate and installation instructions.
Test signing permits the explicitly marked CI media fixture. It does not make
that fixture suitable for Store submission. Private signing keys remain on the
build machine only and are removed when packaging finishes.

## Install the test package

Download the MSIX artifact from the approved Windows CI run. Extract the archive
and read its installation instructions. Compare the certificate's fingerprint
with the build output before trusting it. Install the public certificate into
**Local Computer > Trusted People**, then install the MSIX through App Installer.
Administrator access is needed to trust the test certificate, not to run TDrive.
Never import a private key or place this certificate in Trusted Root authorities.

The laptop needs the Microsoft Evergreen WebView2 Runtime. Wails uses the
installed runtime; MSIX does not run the existing NSIS bootstrapper. If it is
missing, install it from Microsoft's [WebView2 download page](https://developer.microsoft.com/en-us/microsoft-edge/webview2/).

Each CI test build has a fresh temporary certificate. Trust its public
certificate before installing that build, and remove obsolete test certificates
from Trusted People once they are no longer needed. Test-signed packages are
not Store releases and do not receive Store updates before publication.

## Runtime and updates

Windows package identity controls the update policy at runtime. Both Store
installs and sideloaded MSIX installs disable GitHub checks, downloads, executable
replacement and rollback cleanup. The Updates panel opens this app's Store page.
Unpackaged GitHub installs retain the existing signed-manifest updater.

The installed package directory is read-only. Configuration and session data
continue through `backend/datadir`; media scratch files use its cache directory.
Wails already places its Windows WebView2 profile under `%APPDATA%`, rather than
next to the executable. The package does not change that browser profile default.
The manifest disables filesystem virtualization for TDrive's Roaming/Local
AppData directories and WebView2 profile on Windows 11. Windows 10 uses the
broader filesystem-virtualization opt-out. This requires the
`unvirtualizedResources` restricted capability and Windows 10 version 2004 or
newer. Registry virtualization remains enabled.

This is necessary because the packaged GUI and unpackaged CLI/daemon must see
the same `backend.lock` whenever they see the same `tdrive.db`. Otherwise a
new virtualized lock could protect an existing unvirtualized database, allowing
two backend engines to run concurrently. Existing data stays in place, and
Store uninstall does not delete these shared AppData files. Explain that
retention to users and use the app's logout flow to clear session data.

## Validation before publishing

CI validates the package layout and manifest, checks signatures for the test
package and exercises installation on a Windows runner. Physical laptop checks
still need to cover:

- Telegram login, two-step verification, logout and restart.
- File previews, media playback, seeking and native-player shutdown.
- Transfers interrupted by app closure and resumed after restart.
- Mounting a drive and reading/writing through another Windows application.
- Package upgrade with retained login, preferences and recovery state.
- Existing unpackaged installation, single-instance behavior and data locations.
- Uninstall behavior, explaining what Windows removes before using private data.

Use a dedicated test account and sample files. Run the Windows App Certification
Kit before submission. A successful build or runner installation does not prove
Telegram playback, physical-device behavior or Store certification.

## Submit manually

Use a reviewed release and approved media runtime, rebuild without `-TestSign`,
then upload the MSIX to the existing Partner Center product. Prepare screenshots,
description, support URL, privacy policy, age rating and reviewer login
instructions. Explain why the native desktop app needs `runFullTrust` for its
file manager, media player and mounted drive features. Explain that
`unvirtualizedResources` preserves the shared database lock and existing data
across the desktop app and CLI. Certification approval for restricted
capabilities must be obtained through the submission process.

Package versions use four numeric components. Keep the Store version increasing
and keep the app build stamp consistent with the package version. Reserve the
fourth component as zero for Store packages. Test packages use the same identity;
remove a newer test version before installing an older Store version.

This workflow builds artifacts only. It does not submit to Partner Center,
publish a release or grant CI approval automatically.

## References

- [MSIX submission requirements](https://learn.microsoft.com/en-us/windows/apps/publish/publish-your-app/msix/app-package-requirements)
- [Prepare a desktop app for MSIX](https://learn.microsoft.com/en-us/windows/msix/desktop/desktop-to-uwp-prepare)
- [Windows package identity](https://learn.microsoft.com/en-us/windows/win32/api/appmodel/nf-appmodel-getcurrentpackagefullname)
- [WebView2 user data folders](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/user-data-folder)
