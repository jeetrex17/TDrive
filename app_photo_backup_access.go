package main

import "errors"

var errPhotoBackupMyDriveOnly = errors.New("encrypted photo backup is available only in My Drive")

func validatePhotoBackupEncryptionDrive(driveID, personalDriveID int64) error {
	if driveID <= 0 || personalDriveID <= 0 || driveID != personalDriveID {
		return errPhotoBackupMyDriveOnly
	}
	return nil
}

// Show the prerequisite before the user starts backup. The uploader still
// checks the key independently; UI state never authorizes plaintext fallback.
func (a *App) photoBackupAccessState(state PhotoBackupState) PhotoBackupState {
	if !state.Settings.Enabled || state.ManualPaused {
		return state
	}
	status, err := a.encryption.EncryptionStatus()
	if err != nil {
		state.Status.Phase = "paused"
		state.Status.Message = "Could not check encryption. Try again when connected."
		return state
	}
	if !status.PasswordRemembered {
		state.EncryptionRequired = true
		state.Status.Phase = "paused"
		if status.PasswordSet {
			state.Status.Message = "Unlock encryption to back up your photos and videos."
		} else {
			state.Status.Message = "Create an encryption password to back up your photos and videos."
		}
	}
	return state
}
