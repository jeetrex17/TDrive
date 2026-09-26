package main

import "testing"

func TestLoginSuccessEnablesAuthenticatedBackgroundWork(t *testing.T) {
	app := &App{}
	sink := runtimeEventSink{app: app}

	sink.Emit("login-code-required")
	if app.authReady.Load() {
		t.Fatal("background work became ready before login succeeded")
	}

	sink.Emit("login-success", true)
	if !app.authReady.Load() {
		t.Fatal("background work did not become ready after login succeeded")
	}
}

func TestCheckLoginStatusReusesVerifiedProcessState(t *testing.T) {
	app := &App{}
	app.authReady.Store(true)
	if !app.CheckLoginStatus() {
		t.Fatal("login status opened a second client despite verified process state")
	}
}
