package file

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestNormalUploadReportsBoundedStageTimings(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	path := writeTempNamedFile(t, "timed.txt", []byte("payload"))

	var mu sync.Mutex
	stages := make(map[string]int)
	ctx := WithUploadTiming(t.Context(), func(stage string, elapsed time.Duration) {
		if elapsed < 0 {
			t.Errorf("negative duration for %q: %v", stage, elapsed)
		}
		mu.Lock()
		stages[stage]++
		mu.Unlock()
	})

	files, err := svc.Upload(ctx, personalChannelID, []string{path}, []string{""}, false)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("uploaded %d files, want 1", len(files))
	}
	mu.Lock()
	defer mu.Unlock()
	for _, stage := range []string{"slot_wait", "source_prepare", "transfer", "receipt_wait", "projection"} {
		if stages[stage] != 1 {
			t.Errorf("stage %q samples = %d, want 1", stage, stages[stage])
		}
	}
}

func TestUploadTimingDebugAggregationPreservesCaller(t *testing.T) {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	svc, _, _, _ := newTestService(t)
	path := writeTempNamedFile(t, "debug.txt", []byte("payload"))
	var stages []string
	ctx := WithUploadTiming(t.Context(), func(stage string, _ time.Duration) { stages = append(stages, stage) })
	if _, err := svc.Upload(ctx, personalChannelID, []string{path}, []string{""}, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"slot_wait", "source_prepare", "transfer", "receipt_wait", "projection"} {
		found := false
		for _, stage := range stages {
			found = found || stage == want
		}
		if !found {
			t.Errorf("caller did not receive %q: %v", want, stages)
		}
	}
}

func TestUploadBatchTimingUsesBoundedQuantileBuckets(t *testing.T) {
	var timings uploadBatchTimings
	for _, sample := range []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 9 * time.Millisecond} {
		timings.observe("transfer", sample)
	}
	timings.observe("source path must not be a stage", time.Hour)
	stage := timings.stages[3]
	if stage.count != 4 || stage.total != 15*time.Millisecond || stage.max != 9*time.Millisecond {
		t.Fatalf("transfer timing = %+v", stage)
	}
	if got := stage.quantileUpper(50); got != 2*time.Millisecond {
		t.Errorf("p50 upper = %v, want 2ms", got)
	}
	if got := stage.quantileUpper(95); got != 16*time.Millisecond {
		t.Errorf("p95 upper = %v, want 16ms", got)
	}
	for i, name := range uploadStageNames {
		if name == "transfer" {
			continue
		}
		if timings.stages[i].count != 0 {
			t.Errorf("unexpected samples for %q", name)
		}
	}
	timings.log(20*time.Millisecond, 4)
}
