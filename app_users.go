package main

// ResolveUsernames maps Telegram user IDs to display names. Used by the
// frontend uploader-chip cache.
func (a *App) ResolveUsernames(userIDs []int64) (map[string]string, error) {
	return a.userService().ResolveUsernames(a.ctx, userIDs)
}
