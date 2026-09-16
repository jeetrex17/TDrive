package remux

import "slices"

// Matroska records only when a frame is shown. MP4 demands the opposite: the
// samples in a fragment must be in decode order, each carrying a signed offset
// back to its presentation time. For a stream with B-frames those two orders
// differ, so the decode times have to be reconstructed rather than read.
//
// Getting this wrong is the classic remuxing failure: decode times that do not
// increase make a player stall or judder rather than refuse the file outright,
// which makes it easy to ship and hard to diagnose.

// AssignDecodeTimes fills in DTS for a run of samples that arrive in decode
// order carrying presentation times.
//
// The reconstruction rests on one fact: whatever order frames are decoded in,
// they are *shown* in ascending time order. So the sequence of decode times is
// the same set of timestamps sorted ascending, slid earlier by just enough that
// no frame is ever scheduled to decode after it was due to be shown.
//
// A stream without B-frames sorts to itself and needs no slide, so its decode
// times come out equal to its presentation times, which is exactly right.
//
// Samples are modified in place.
func AssignDecodeTimes(samples []Sample) {
	if len(samples) == 0 {
		return
	}

	ordered := make([]int64, len(samples))
	for i, sample := range samples {
		ordered[i] = sample.PTS
	}
	slices.Sort(ordered)

	// How far the whole schedule must move earlier so that every frame decodes
	// no later than it is shown. With no reordering this is zero.
	var slide int64
	for i, sample := range samples {
		if gap := ordered[i] - sample.PTS; gap > slide {
			slide = gap
		}
	}

	for i := range samples {
		samples[i].DTS = ordered[i] - slide
	}
}

// ReorderDepth reports how many frames the stream holds before it can show one,
// which is the number of frames whose presentation time precedes that of a
// frame decoded earlier.
//
// It is only a diagnostic. The reconstruction above does not need it, which is
// deliberate: deriving the depth from the sequence itself avoids trusting a
// declared value that some encoders get wrong.
func ReorderDepth(samples []Sample) int {
	depth := 0
	var highest int64
	for i, sample := range samples {
		if i == 0 || sample.PTS > highest {
			highest = sample.PTS
			continue
		}
		// This frame is shown before one already seen, so at least one frame is
		// being held back. Count how many earlier frames it overtakes.
		held := 0
		for j := i - 1; j >= 0 && samples[j].PTS > sample.PTS; j-- {
			held++
		}
		if held > depth {
			depth = held
		}
	}
	return depth
}

// MonotonicDecodeTimes reports whether decode times never go backwards, which
// is what a player requires and what a mistaken reconstruction breaks.
func MonotonicDecodeTimes(samples []Sample) bool {
	for i := 1; i < len(samples); i++ {
		if samples[i].DTS < samples[i-1].DTS {
			return false
		}
	}
	return true
}

// DecodeBeforePresentation reports whether every frame is scheduled to decode
// no later than it is shown, which MP4 requires: the composition offset it
// stores cannot be negative in the version of the box written here.
func DecodeBeforePresentation(samples []Sample) bool {
	for _, sample := range samples {
		if sample.DTS > sample.PTS {
			return false
		}
	}
	return true
}
