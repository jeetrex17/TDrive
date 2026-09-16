package remux

import "errors"

// AC-3 and E-AC-3 are the only formats Matroska carries with no CodecPrivate,
// so their MP4 decoder configuration boxes (dac3, dec3) have to be recovered by
// reading the first syncframe header. Everything here follows ATSC A/52, which
// defines that header.
//
// This is a deliberately small reimplementation rather than a dependency: the
// one library that offers it pins the whole module to a newer Go toolchain than
// the project uses, which is a poor trade for sixty lines of bit reading.

var errShortSyncframe = errors.New("remux: audio frame too short to hold a syncframe header")

var errNotSyncframe = errors.New("remux: no AC-3 syncword at the start of the frame")

// AC3Config is what dac3 and dec3 need to describe the stream.
type AC3Config struct {
	// SampleRateCode selects 48, 44.1 or 32 kHz.
	SampleRateCode uint8
	// BitstreamID distinguishes AC-3 (8) from E-AC-3 (16).
	BitstreamID uint8
	// BitstreamMode and ChannelMode describe the service and the speaker layout.
	BitstreamMode uint8
	ChannelMode   uint8
	// LFEOn reports the low-frequency effects channel.
	LFEOn bool
	// BitRateCode is the nominal bitrate, derived from the frame size code.
	BitRateCode uint8
}

// SampleRate resolves SampleRateCode to Hz. A reserved code reports 0.
func (c AC3Config) SampleRate() int {
	switch c.SampleRateCode {
	case 0:
		return 48000
	case 1:
		return 44100
	case 2:
		return 32000
	default:
		return 0
	}
}

// ChannelCount counts the coded channels, including LFE.
func (c AC3Config) ChannelCount() int {
	// acmod values are the standard A/52 speaker layouts: dual mono, mono,
	// stereo, 3 front, 2 front + 1 surround, and so on.
	channels := []int{2, 1, 2, 3, 3, 4, 4, 5}
	count := 0
	if int(c.ChannelMode) < len(channels) {
		count = channels[c.ChannelMode]
	}
	if c.LFEOn {
		count++
	}
	return count
}

// bitReader walks a byte slice a bit at a time, most significant bit first,
// which is how A/52 lays out its header fields.
type bitReader struct {
	data []byte
	pos  int // in bits
}

func (r *bitReader) read(bits int) uint32 {
	var out uint32
	for i := range bits {
		byteIndex := r.pos >> 3
		if byteIndex >= len(r.data) {
			return out << (bits - i)
		}
		bit := (r.data[byteIndex] >> (7 - uint(r.pos&7))) & 1
		out = out<<1 | uint32(bit)
		r.pos++
	}
	return out
}

// ParseAC3Syncframe reads the header of an AC-3 or E-AC-3 frame.
//
// The two formats share a syncword but diverge immediately after it: AC-3 puts
// the sample rate and frame size first, while E-AC-3 leads with a stream type
// and frame size and carries its sample rate later. bsid tells them apart, but
// bsid itself sits at different offsets, so each layout is read separately.
func ParseAC3Syncframe(frame []byte) (AC3Config, error) {
	if len(frame) < 8 {
		return AC3Config{}, errShortSyncframe
	}
	if frame[0] != 0x0B || frame[1] != 0x77 {
		return AC3Config{}, errNotSyncframe
	}

	// bsid lives at bit 40 in AC-3 and bit 21 in E-AC-3, but both encode it as
	// five bits and E-AC-3 is defined as bsid > 10. Read the AC-3 position
	// first; if it says E-AC-3, re-read with the other layout.
	probe := &bitReader{data: frame}
	probe.read(16) // syncword
	probe.read(16) // crc1
	probe.read(2)  // fscod
	probe.read(6)  // frmsizecod
	bsid := uint8(probe.read(5))

	if bsid > 10 {
		return parseEAC3(frame)
	}
	return parseAC3(frame, bsid)
}

func parseAC3(frame []byte, bsid uint8) (AC3Config, error) {
	r := &bitReader{data: frame}
	r.read(16) // syncword
	r.read(16) // crc1
	fscod := uint8(r.read(2))
	frmsizecod := uint8(r.read(6))
	r.read(5) // bsid, already read
	bsmod := uint8(r.read(3))
	acmod := uint8(r.read(3))

	// The mix level fields are present only for the layouts that need them.
	if acmod&0x1 != 0 && acmod != 0x1 {
		r.read(2) // cmixlev
	}
	if acmod&0x4 != 0 {
		r.read(2) // surmixlev
	}
	if acmod == 0x2 {
		r.read(2) // dsurmod
	}
	lfeon := r.read(1) == 1

	return AC3Config{
		SampleRateCode: fscod,
		BitstreamID:    bsid,
		BitstreamMode:  bsmod,
		ChannelMode:    acmod,
		LFEOn:          lfeon,
		// dac3 carries the nominal bitrate, which is the frame size code
		// without its half-frame bit.
		BitRateCode: frmsizecod >> 1,
	}, nil
}

func parseEAC3(frame []byte) (AC3Config, error) {
	r := &bitReader{data: frame}
	r.read(16) // syncword
	r.read(2)  // strmtyp
	r.read(3)  // substreamid
	r.read(11) // frmsiz
	fscod := uint8(r.read(2))
	if fscod == 3 {
		// A reduced sample rate stream codes the real rate in the next field.
		fscod2 := uint8(r.read(2))
		if fscod2 == 3 {
			return AC3Config{}, errNotSyncframe
		}
		fscod = fscod2
	} else {
		r.read(2) // numblkscod
	}
	acmod := uint8(r.read(3))
	lfeon := r.read(1) == 1
	bsid := uint8(r.read(5))

	return AC3Config{
		SampleRateCode: fscod,
		BitstreamID:    bsid,
		// E-AC-3's header has no bsmod in this position; the complete service
		// type lives in a later, optional field. Main audio service is the
		// correct default and what every remuxer writes.
		BitstreamMode: 0,
		ChannelMode:   acmod,
		LFEOn:         lfeon,
		BitRateCode:   0,
	}, nil
}
