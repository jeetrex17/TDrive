package main

import (
	"strings"
	"testing"
)

func TestMachineHelpTopics(t *testing.T) {
	for _, topic := range []string{"version", "commands"} {
		t.Run(topic, func(t *testing.T) {
			key := canonicalHelpKey([]string{topic})
			if _, ok := helpTopics[key]; !ok {
				t.Fatalf("missing help topic %q", key)
			}
		})
	}

	commands := helpTopics["commands"]
	for _, expected := range []string{"tdrive commands --json", "--drive-id", "--non-interactive", "schema_version", "Exit codes"} {
		if !strings.Contains(strings.ToLower(commands), strings.ToLower(expected)) {
			t.Errorf("machine help does not describe %q", expected)
		}
	}
}

func TestRequestedHelpHonorsOptionTerminator(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "help flag", args: []string{"ls", "--help"}, want: true},
		{name: "filename after terminator", args: []string{"ls", "--", "--help"}},
		{name: "first positional after terminator", args: []string{"--", "--help"}},
		{name: "help before terminator", args: []string{"ls", "--help", "--", "filename"}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, got := requestedHelp(test.args)
			if got != test.want {
				t.Fatalf("requestedHelp(%q) = %t, want %t", test.args, got, test.want)
			}
		})
	}
}
