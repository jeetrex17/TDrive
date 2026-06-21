package main

import "testing"

func TestValidateNativeMediaCommandAcceptsSupportedCommands(t *testing.T) {
	tests := [][]string{
		{"cycle", "pause"},
		{"cycle", "mute"},
		{"seek", "10", "relative"},
		{"seek", "-5.5", "relative"},
	}

	for _, command := range tests {
		if err := validateNativeMediaCommand(command); err != nil {
			t.Fatalf("validateNativeMediaCommand(%v) returned error: %v", command, err)
		}
	}
}

func TestValidateNativeMediaCommandRejectsUnsupportedCommands(t *testing.T) {
	tests := [][]string{
		nil,
		{},
		{"loadfile", "https://example.com/video.mkv"},
		{"screenshot-to-file", "/tmp/frame.png"},
		{"cycle", "fullscreen"},
		{"cycle", "pause", "extra"},
		{"seek", "10", "absolute"},
		{"seek", "soon", "relative"},
	}

	for _, command := range tests {
		if err := validateNativeMediaCommand(command); err == nil {
			t.Fatalf("validateNativeMediaCommand(%v) returned nil error", command)
		}
	}
}
