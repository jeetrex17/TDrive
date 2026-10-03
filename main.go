package main

import (
	"embed"
	"log"

	"TDrive/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

// appVersion is stamped by release builds with -X main.appVersion.
var appVersion = "dev"

func main() {
	if err := app.Run(assets, appVersion); err != nil {
		log.Fatal(err)
	}
}
