package main

import (
	"errors"
	"testing"
	"time"
)

func TestGalleryImagesQueuedOpenCannotRecreateLoggedOutScope(t *testing.T) {
	app := &App{ctx: t.Context()}
	if err := app.mountLifecycle.Lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := app.OpenGalleryImages(1); done <- err }()
	// Logout marks the gate terminal before releasing queued bridge calls.
	app.mountLifecycleTerminal = true
	app.mountLifecycle.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, errAppMountLifecycleTerminal) {
			t.Fatalf("open afterlogout=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued image scope remained blocked")
	}
	if app.galleryImages != nil {
		t.Fatal("logged-out request created loopback listener")
	}
}
