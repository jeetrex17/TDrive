package main

// Show the prerequisite before the user starts backup. The uploader still
// checks the key independently; UI state never authorizes plaintext fallback.
func (a *App) photoBackupAccessState(state PhotoBackupState) PhotoBackupState {
	if !state.Settings.Enabled || !state.Settings.Encrypt || state.ManualPaused {
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
		state.Status.Message = "Unlock encryption to back up your photos and videos."
	}
	return state
}
