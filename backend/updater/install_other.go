//go:build !darwin && !windows && !linux

package updater

type unsupportedInstaller struct{}

func newPlatformInstaller() Installer { return unsupportedInstaller{} }

func (unsupportedInstaller) Target() (Target, error) {
	return Target{}, &NotInstallableError{Reason: "Automatic updates aren't supported on this platform."}
}

func (unsupportedInstaller) Install(string, Target) error {
	return &NotInstallableError{Reason: "Automatic updates aren't supported on this platform."}
}

func (unsupportedInstaller) Relaunch(Target, int) error { return nil }

func (unsupportedInstaller) Cleanup(Target) error { return nil }
