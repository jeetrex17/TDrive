package auth

import (
	"TDrive/backend/auth"
)

func createChannel() {
	tgclient, err := auth.Connect()
	if err != nil {
		return err
	}
}
