package remux

import "testing"

// samplesWithPTS builds a run in decode order carrying the given presentation
// times, which is exactly the shape Matroska hands over.
func samplesWithPTS(pts ...int64) []Sample {
	samples := make([]Sample, len(pts))
	for i, value := range pts {
		samples[i] = Sample{PTS: value, Sync: i == 0}
	}
	return samples
}

func TestNoReorderingLeavesDecodeTimesEqualToPresentation(t *testing.T) {
	// A stream with no B-frames is shown in the order it is decoded, so
	// nothing should move.
	samples := samplesWithPTS(0, 100, 200, 300, 400)
	AssignDecodeTimes(samples)

	for i, sample := range samples {
		if sample.DTS != sample.PTS {
			t.Errorf("sample %d: dts %d, want %d", i, sample.DTS, sample.PTS)
		}
		if sample.CompositionOffset() != 0 {
			t.Errorf("sample %d: composition offset %d, want 0", i, sample.CompositionOffset())
		}
	}
}

func TestClassicIBBPPatternGetsUsableDecodeTimes(t *testing.T) {
	// IBBP: the P frame is decoded before the two B frames that precede it on
	// screen, so decode order carries presentation times out of sequence.
	samples := samplesWithPTS(0, 300, 100, 200)
	AssignDecodeTimes(samples)

	if !MonotonicDecodeTimes(samples) {
		t.Fatalf("decode times went backwards: %v", decodeTimes(samples))
	}
	if !DecodeBeforePresentation(samples) {
		t.Fatalf("a frame decodes after it is shown: %v", samples)
	}
}

func TestLongerReorderRunStaysValid(t *testing.T) {
	// A deeper hierarchy, the kind a Blu-ray encode produces.
	samples := samplesWithPTS(0, 800, 400, 200, 600, 1600, 1200, 1000, 1400)
	AssignDecodeTimes(samples)

	if !MonotonicDecodeTimes(samples) {
		t.Fatalf("decode times went backwards: %v", decodeTimes(samples))
	}
	if !DecodeBeforePresentation(samples) {
		t.Fatal("a frame decodes after it is shown")
	}
	// Every presentation time must survive untouched: this reconstruction may
	// only add decode times, never move a frame on screen.
	for i, want := range []int64{0, 800, 400, 200, 600, 1600, 1200, 1000, 1400} {
		if samples[i].PTS != want {
			t.Errorf("sample %d: presentation time changed to %d, want %d", i, samples[i].PTS, want)
		}
	}
}

func TestDecodeTimesUseEveryTimestampExactlyOnce(t *testing.T) {
	// The decode schedule is the same set of instants, reordered and slid. It
	// must not invent or drop one, or the track drifts against its audio.
	samples := samplesWithPTS(0, 300, 100, 200, 700, 500, 400, 600)
	AssignDecodeTimes(samples)

	gaps := map[int64]int{}
	for i := 1; i < len(samples); i++ {
		gaps[samples[i].DTS-samples[i-1].DTS]++
	}
	// Uniformly spaced input must produce uniformly spaced decode times.
	if len(gaps) != 1 {
		t.Errorf("decode times are unevenly spaced: %v", gaps)
	}
}

func TestReorderDepthCountsFramesHeldBack(t *testing.T) {
	if got := ReorderDepth(samplesWithPTS(0, 100, 200)); got != 0 {
		t.Errorf("depth of a stream with no reordering = %d, want 0", got)
	}
	// IBBP holds exactly one frame: the P is decoded and kept while the two B
	// frames that precede it on screen are decoded. That is the same value a
	// decoder reports as its reorder depth for this pattern.
	if got := ReorderDepth(samplesWithPTS(0, 300, 100, 200)); got != 1 {
		t.Errorf("depth of IBBP = %d, want 1", got)
	}
	// A deeper hierarchy holds more: here two frames are outstanding.
	if got := ReorderDepth(samplesWithPTS(0, 800, 400, 200, 600)); got < 2 {
		t.Errorf("depth of a hierarchical GOP = %d, want at least 2", got)
	}
}

func TestAssignDecodeTimesHandlesEmptyAndSingle(t *testing.T) {
	AssignDecodeTimes(nil)

	one := samplesWithPTS(500)
	AssignDecodeTimes(one)
	if one[0].DTS != 500 {
		t.Errorf("single sample dts = %d, want 500", one[0].DTS)
	}
}

func TestMonotonicCheckCatchesABadSchedule(t *testing.T) {
	// The guard has to fail on the thing it exists to catch, or it is decoration.
	bad := []Sample{{PTS: 0, DTS: 100}, {PTS: 100, DTS: 0}}
	if MonotonicDecodeTimes(bad) {
		t.Error("accepted decode times that go backwards")
	}
	if DecodeBeforePresentation([]Sample{{PTS: 0, DTS: 100}}) {
		t.Error("accepted a frame that decodes after it is shown")
	}
}

func decodeTimes(samples []Sample) []int64 {
	out := make([]int64, len(samples))
	for i, sample := range samples {
		out[i] = sample.DTS
	}
	return out
}
