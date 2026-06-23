package main

// ResolveUsernames maps Telegram user IDs to display names. Used by the
// frontend uploader-chip cache.
func (a *App) ResolveUsernames(userIDs []int64) (map[string]string, error) {
	svc, err := a.requireUserService()
	if err != nil {
		return nil, err
	}
	return svc.ResolveUsernames(a.ctx, userIDs)
}
