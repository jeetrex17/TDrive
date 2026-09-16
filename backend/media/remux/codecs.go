// Package remux repackages Matroska into fragmented MP4 so iOS can play it.
//
// Apple ships no Matroska demuxer, so an .mkv cannot be opened on an iPhone
// whatever is inside it. The video and audio streams inside, however, are
// usually formats iOS decodes perfectly well. Repackaging them into fragmented
// MP4 and serving that as HLS costs no re-encoding: the frames are copied
// byte-for-byte and only the container around them changes.
//
// Delivery is HLS rather than one progressive file, and that is forced rather
// than chosen. A progressive fragmented MP4 is seeked by byte offset, which a
// player resolves from a `sidx` box listing every fragment's compressed size.
// Those sizes are unknowable until the whole file has been muxed, so an
// on-demand remuxer cannot publish one without first pulling the entire source
// from Telegram. HLS needs no such index: the player finds a segment by adding
// up advertised durations, and each segment maps to one Matroska cluster range,
// which is one Telegram range read.
package remux

import "strings"

// VideoCodec and AudioCodec name the elementary stream inside a Matroska track,
// in the terms MP4 uses for it.
type VideoCodec string

type AudioCodec string

const (
	VideoUnknown VideoCodec = ""
	VideoAVC     VideoCodec = "avc1" // H.264
	VideoHEVC    VideoCodec = "hvc1" // H.265
)

const (
	AudioUnknown AudioCodec = ""
	AudioAAC     AudioCodec = "mp4a"
	AudioAC3     AudioCodec = "ac-3"
	AudioEAC3    AudioCodec = "ec-3"
	AudioFLAC    AudioCodec = "fLaC"
	AudioALAC    AudioCodec = "alac"
)

// videoCodecs maps Matroska CodecIDs to the MP4 sample entry to write.
//
// HEVC is tagged hvc1 and never hev1. Apple's authoring rules say to prefer
// hvc1, and in practice Safari refuses hev1-tagged streams outright, so the
// distinction is not stylistic.
var videoCodecs = map[string]VideoCodec{
	"V_MPEG4/ISO/AVC":  VideoAVC,
	"V_MPEGH/ISO/HEVC": VideoHEVC,
}

// audioCodecs maps Matroska CodecIDs to the MP4 sample entry to write.
//
// Only formats iOS can decode *and* Apple sanctions inside MP4 appear here.
// Notably absent, with reasons:
//
//   - DTS, DTS-HD MA, TrueHD/MLP: iOS has no decoder for these in any
//     container, so there is nothing a remux could do.
//   - MP3: the device decodes it, but MP3-in-MP4 is absent from Apple's codec
//     lists and is not a combination they support.
//   - Opus, Vorbis: Opus in MP4 is unreliable on iOS and explicitly unsupported
//     in HLS fragmented MP4; Vorbis is not carried in MP4 on Apple platforms at
//     all.
//
// A file whose only audio is one of those plays video-only or not at all, and
// the caller is expected to say so rather than play silence.
var audioCodecs = map[string]AudioCodec{
	"A_AAC":     AudioAAC,
	"A_AC3":     AudioAC3,
	"A_EAC3":    AudioEAC3,
	"A_FLAC":    AudioFLAC,
	"A_ALAC":    AudioALAC,
	"A_MPEG/L3": AudioUnknown, // MP3: decodable, but not sanctioned in MP4
	"A_DTS":     AudioUnknown,
	"A_TRUEHD":  AudioUnknown,
	"A_OPUS":    AudioUnknown,
	"A_VORBIS":  AudioUnknown,
}

// VideoCodecFor resolves a Matroska CodecID to the sample entry to write, or
// VideoUnknown when the stream cannot be copied into MP4.
//
// Matroska CodecIDs are matched on their prefix: AAC in particular carries
// profile suffixes like A_AAC/MPEG4/LC that name the same elementary stream.
func VideoCodecFor(codecID string) VideoCodec {
	id := strings.ToUpper(strings.TrimSpace(codecID))
	if codec, ok := videoCodecs[id]; ok {
		return codec
	}
	for prefix, codec := range videoCodecs {
		if strings.HasPrefix(id, prefix+"/") {
			return codec
		}
	}
	return VideoUnknown
}

// AudioCodecFor resolves a Matroska CodecID the same way. An entry that maps to
// AudioUnknown is listed deliberately: it means "known format, cannot be
// copied", which is different from "unrecognised".
func AudioCodecFor(codecID string) AudioCodec {
	id := strings.ToUpper(strings.TrimSpace(codecID))
	if codec, ok := audioCodecs[id]; ok {
		return codec
	}
	for prefix, codec := range audioCodecs {
		if strings.HasPrefix(id, prefix+"/") {
			return codec
		}
	}
	return AudioUnknown
}

// NeedsSyncframeProbe reports whether the track's decoder configuration has to
// be recovered from the first audio frame rather than read from CodecPrivate.
//
// Matroska stores the MP4 decoder configuration verbatim in CodecPrivate for
// most formats, which makes building avcC, hvcC, esds, dfLa and so on a byte
// copy. AC-3 and E-AC-3 are the exception: the spec gives them no CodecPrivate,
// so dac3 and dec3 must be filled by parsing the first syncframe header.
func NeedsSyncframeProbe(codec AudioCodec) bool {
	return codec == AudioAC3 || codec == AudioEAC3
}
