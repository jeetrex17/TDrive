//go:build darwin || linux

package mountos

import (
	"context"
	"testing"
)

func TestExecCommandRunnerUsesArgvAndBoundsOutput(t *testing.T) {
	t.Parallel()

	runner := execCommandRunner{}
	if err := runner.Run(context.Background(), newCommandPlan("/usr/bin/true")); err != nil {
		t.Fatalf("Run(true) error = %v", err)
	}
	output, err := runner.Output(context.Background(), newCommandPlan("/usr/bin/printf", "%s", "safe output"), 64)
	if err != nil || string(output) != "safe output" {
		t.Fatalf("Output(printf) = %q, %v", output, err)
	}
	if _, err := runner.Output(context.Background(), newCommandPlan("/usr/bin/printf", "%s", "too long"), 3); err == nil {
		t.Fatal("Output() unexpectedly accepted output over its bound")
	}
	if _, err := runner.Output(context.Background(), newCommandPlan("/usr/bin/true"), 0); err == nil {
		t.Fatal("Output() unexpectedly accepted a zero bound")
	}
}
