package remux

import (
	"fmt"
	"math"
	"time"
)

func (f *File) readTracks(r *ebmlReader, end int64) error {
	err := r.walk(end, func(id uint32, size int64) error {
		if id != idTrackEntry {
			return nil
		}
		track, encoded, err := readTrackEntry(r, r.pos+size)
		if err != nil || track.Kind == 0 {
			return err
		}
		if encoded.refuse {
			// Copying a frame this package cannot undo produces a file that
			// opens cleanly and then decodes to noise, which is far worse than
			// refusing the track. Clearing the codec keeps it out of BestAudio
			// and FirstVideo; the number is remembered so a caller that asks
			// for it anyway gets an error rather than rubbish.
			track.Video, track.Audio = VideoUnknown, AudioUnknown
		}
		if encoded.refuse || len(encoded.prefix) > 0 {
			f.encodings[track.Number] = encoded
		}
		f.tracks = append(f.tracks, track)
		return nil
	})
	if err != nil {
		return fmt.Errorf("remux: reading tracks: %w", err)
	}
	return nil
}

func readTrackEntry(r *ebmlReader, end int64) (Track, encoding, error) {
	track := Track{SampleRate: defaultSampleRate, Channels: 1, Language: defaultLanguage}
	var trackType uint64
	var codecID string
	var frequency float64
	var encoded encoding

	err := r.walk(end, func(id uint32, size int64) error {
		var err error
		switch id {
		case idTrackNumber:
			track.Number, err = r.uintValue(size)
		case idTrackType:
			trackType, err = r.uintValue(size)
		case idDefaultDuration:
			var ns uint64
			ns, err = r.uintValue(size)
			track.DefaultDuration = time.Duration(ns)
		case idCodecID:
			codecID, err = r.stringValue(size)
		case idCodecPrivate:
			// Carried through byte for byte: for H.264 and HEVC this is already
			// the exact payload of avcC and hvcC.
			track.CodecPrivate, err = r.binaryValue(size)
		case idTrackName:
			track.Name, err = r.stringValue(size)
		case idLanguage:
			track.Language, err = r.stringValue(size)
		case idContentEncodings:
			encoded, err = readContentEncodings(r, r.pos+size)
		case idVideo:
			err = r.walk(r.pos+size, func(id uint32, size int64) error {
				var err error
				switch id {
				case idPixelWidth:
					track.Width, err = r.uintValue(size)
				case idPixelHeight:
					track.Height, err = r.uintValue(size)
				}
				return err
			})
		case idAudio:
			err = r.walk(r.pos+size, func(id uint32, size int64) error {
				var err error
				switch id {
				case idSamplingFrequency:
					frequency, err = r.floatValue(size)
				case idChannels:
					var channels uint64
					channels, err = r.uintValue(size)
					track.Channels = uint16(channels)
				}
				return err
			})
		}
		return err
	})
	if err != nil {
		return Track{}, encoding{}, err
	}

	switch trackType {
	case trackTypeVideo:
		track.Kind = TrackVideo
		track.Video = VideoCodecFor(codecID)
		track.Timescale = videoTimescale(track.DefaultDuration)
		// The audio defaults seeded above would otherwise describe a video
		// track as 8 kHz mono.
		track.SampleRate, track.Channels = 0, 0
	case trackTypeAudio:
		track.Kind = TrackAudio
		track.Audio = AudioCodecFor(codecID)
		if frequency > 0 {
			track.SampleRate = uint32(math.Round(frequency))
		}
		// Sample rate is the one timescale an audio track can have without
		// rounding a frame boundary, since every frame is a whole number of
		// samples.
		track.Timescale = track.SampleRate
	}
	return track, encoded, nil
}

// encoding is what a track's ContentEncodings element asks a reader to do to a
// frame before the frame means anything.
type encoding struct {
	// prefix is the run of bytes header stripping took off the front of every
	// frame, to be put back.
	prefix []byte
	// refuse marks an encoding this package will not undo.
	refuse bool
}

// compHeaderStripping is the one ContentEncoding undone here, and it is not
// compression at all: the muxer noticed every frame of a track opening with the
// same bytes and dropped them, leaving the bytes themselves in
// ContentCompSettings. Older mkvmerge turns it on by default for some audio
// tracks, so refusing it would tell a viewer their film has no playable sound
// when it has perfectly good AC-3 a byte copy away.
const compHeaderStripping = 3

// readContentEncodings reads what was done to a track's frames.
//
// Everything but header stripping is refused. zlib, bzlib and lzo1x are real
// compression, rare enough in the wild that carrying a decompressor for them is
// a poor trade, and encryption is not this package's business at all. The
// defaults Matroska gives these fields all point at compression, so an element
// this reader did not understand fails closed rather than silently copying
// frames it has not undone.
func readContentEncodings(r *ebmlReader, end int64) (encoding, error) {
	var out encoding
	err := r.walk(end, func(id uint32, size int64) error {
		if id != idContentEncoding {
			return nil
		}
		var kind, algo uint64
		scope := uint64(1)
		var settings []byte
		err := r.walk(r.pos+size, func(id uint32, size int64) error {
			var err error
			switch id {
			case idContentEncodingScope:
				scope, err = r.uintValue(size)
			case idContentEncodingType:
				kind, err = r.uintValue(size)
			case idContentCompression:
				err = r.walk(r.pos+size, func(id uint32, size int64) error {
					var err error
					switch id {
					case idContentCompAlgo:
						algo, err = r.uintValue(size)
					case idContentCompSettings:
						settings, err = r.binaryValue(size)
					}
					return err
				})
			}
			return err
		})
		if err != nil {
			return err
		}
		// Scope says what the encoding was applied to. One that spares the
		// frames still covers the codec private data, which is copied into the
		// MP4 sample entry verbatim, so it is no more usable.
		if kind != 0 || algo != compHeaderStripping || scope&1 == 0 {
			out.refuse = true
			return nil
		}
		out.prefix = settings
		return nil
	})
	return out, err
}

// videoTimescale picks a tick rate that divides the frame interval evenly.
//
// Carrying Matroska's milliseconds forward puts 23.976 frames per second on a
// grid it does not fit: 1001/24000 of a second is 41.708 ms, and a frame
// duration rounded to 42 ms drifts by a frame every twenty minutes. Rounding
// the rate instead and scaling by a thousand lands on 24000, where the interval
// is exactly 1001 ticks. The same falls out for 29.97 and 59.94.
func videoTimescale(frame time.Duration) uint32 {
	if frame <= 0 {
		return defaultVideoTimescale
	}
	rate := math.Round(float64(time.Second) / float64(frame))
	if rate < 1 || rate > 1000 {
		return defaultVideoTimescale
	}
	return uint32(rate) * 1000
}
