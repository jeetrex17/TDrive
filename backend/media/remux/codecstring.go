package remux

import (
	"fmt"
	"strings"
)

// The CODECS attribute is how a player decides whether it can play a stream
// before fetching any of it.
//
// AVFoundation takes it seriously: a stream it cannot decode is skipped without
// a request, which is the behaviour that makes an HLS master playlist useful.
// The same seriousness is why these strings are derived from the decoder
// configuration in the file rather than guessed from the codec name. A wrong
// string is worse than none, because it is believed.

// VideoCodecString builds the RFC 6381 name for a video track.
//
// An empty result means the configuration could not be read, and the caller
// should leave the codec out rather than substitute something plausible.
func VideoCodecString(track Track) string {
	switch track.Video {
	case VideoAVC:
		return avcCodecString(track.CodecPrivate)
	case VideoHEVC:
		return hevcCodecString(track.CodecPrivate)
	default:
		return ""
	}
}

// avcCodecString reads the profile, constraints and level straight out of the
// avcC, where they sit in the three bytes after the configuration version.
func avcCodecString(config []byte) string {
	if len(config) < 4 {
		return ""
	}
	return fmt.Sprintf("avc1.%02x%02x%02x", config[1], config[2], config[3])
}

// hevcCodecString assembles the name from the profile tier level record that
// opens an hvcC.
//
// The format is fixed by ISO/IEC 14496-15: profile space and profile, then the
// compatibility flags with their bit order reversed, then the tier and level,
// then the constraint bytes. Trailing zero fields are dropped, which is what
// makes two writers agree on the same stream.
func hevcCodecString(config []byte) string {
	if len(config) < 13 || config[0] != 1 {
		return ""
	}

	profileSpace := (config[1] >> 6) & 0x03
	tier := (config[1] >> 5) & 0x01
	profile := config[1] & 0x1F
	compatibility := uint32(config[2])<<24 | uint32(config[3])<<16 | uint32(config[4])<<8 | uint32(config[5])
	level := config[12]

	// Apple's rule, and the reason hev1 is not used here: hvc1 promises the
	// parameter sets are in the sample entry rather than in the stream.
	name := "hvc1."
	if profileSpace > 0 {
		name += string(rune('A' + profileSpace - 1))
	}
	name += fmt.Sprintf("%d.%X.", profile, reverseBits(compatibility))
	if tier == 1 {
		name += "H"
	} else {
		name += "L"
	}
	name += fmt.Sprintf("%d", level)

	// The six constraint bytes are written most significant first with trailing
	// zero bytes left off entirely.
	constraints := config[6:12]
	last := len(constraints)
	for last > 0 && constraints[last-1] == 0 {
		last--
	}
	for _, b := range constraints[:last] {
		name += fmt.Sprintf(".%X", b)
	}
	return name
}

// reverseBits puts the HEVC compatibility flags in the order the codec string
// wants, which is the reverse of the order they are stored in.
func reverseBits(value uint32) uint32 {
	var out uint32
	for i := 0; i < 32; i++ {
		out = out<<1 | (value>>i)&1
	}
	return out
}

// AudioCodecString builds the RFC 6381 name for an audio track.
//
// Only AAC carries anything variable: its object type lives in the first five
// bits of the AudioSpecificConfig, and low complexity against high efficiency
// is a difference a player acts on. The rest name themselves.
func AudioCodecString(track Track) string {
	switch track.Audio {
	case AudioAAC:
		return aacCodecString(track.CodecPrivate)
	case AudioAC3, AudioEAC3, AudioFLAC, AudioALAC:
		return string(track.Audio)
	default:
		return ""
	}
}

func aacCodecString(config []byte) string {
	objectType := 2 // Low complexity, which is what the overwhelming majority of files carry.
	if len(config) > 0 {
		if declared := int(config[0] >> 3); declared > 0 && declared != 31 {
			objectType = declared
		}
	}
	return fmt.Sprintf("mp4a.40.%d", objectType)
}

// CodecsAttribute joins the names of everything a variant plays, leaving out
// any track whose configuration could not be read rather than describing it
// wrongly.
func CodecsAttribute(tracks ...Track) string {
	names := make([]string, 0, len(tracks))
	for _, track := range tracks {
		name := VideoCodecString(track)
		if track.Kind == TrackAudio {
			name = AudioCodecString(track)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ",")
}
