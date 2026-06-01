package tgclient

import "TDrive/backend/auth"

func ParseInviteHash(link string) (string, error) {
	return auth.ParseInviteHash(link)
}
