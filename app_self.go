package main

// SelfUser is a small projection of the logged-in Telegram user, shaped
// for the frontend profile menu. Photo download is best-effort; an empty
// PhotoBase64 means "no photo available" and the UI should fall back to
// initials.
type SelfUser struct {
	UserID      int64  `json:"user_id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username,omitempty"`
	PhotoBase64 string `json:"photo_base64,omitempty"`
}

// Me resolves the logged-in Telegram user and a small base64-encoded
// avatar photo. The result is cached by the user service for the lifetime
// of the process; logout clears it.
func (a *App) Me() (SelfUser, error) {
	me, err := a.userService().Me(a.ctx)
	if err != nil {
		return SelfUser{}, err
	}
	return SelfUser{
		UserID:      me.UserID,
		DisplayName: me.DisplayName,
		Username:    me.Username,
		PhotoBase64: me.PhotoBase64,
	}, nil
}
