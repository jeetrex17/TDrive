package remux

import (
	"bytes"
	"fmt"
	"time"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// The MP4 half of the remux: one init segment that describes the tracks, then
// one fragment per HLS segment carrying their samples.
//
// Tracks are identified by position, not by number. MP4 numbers tracks from one
// in the order they appear in the moov, while Matroska numbers them however the
// file pleases, so track i in the slice is MP4 trackID i+1. Both functions must
// be handed the same slice in the same order or a segment addresses a track the
// init segment never described.

// InitSegment builds the payload an HLS EXT-X-MAP points at: ftyp, and a moov
// whose mvex declares the file fragmented and whose traks carry the decoder
// configuration for each stream.
//
// firstFrames is keyed by Track.Number and is read only for AC-3 and E-AC-3,
// the two formats Matroska stores no CodecPrivate for. Their configuration
// comes from a syncframe header instead.
func InitSegment(tracks []Track, firstFrames map[uint64][]byte) ([]byte, error) {
	if len(tracks) == 0 {
		return nil, fmt.Errorf("%w: no tracks to describe", ErrUnsupported)
	}

	init := mp4.CreateEmptyInit()
	for _, track := range tracks {
		if track.Timescale == 0 {
			return nil, fmt.Errorf("%w: track %d has no timescale", ErrUnsupported, track.Number)
		}
		mediaType := "video"
		if track.Kind == TrackAudio {
			mediaType = "audio"
		}
		trak := init.AddEmptyTrack(track.Timescale, mediaType, mdhdLanguage(track.Language))
		if err := describeTrack(trak, track, firstFrames[track.Number]); err != nil {
			return nil, fmt.Errorf("track %d: %w", track.Number, err)
		}
	}

	// Nothing measures anything against the movie timescale here, because every
	// sample time is in its own track's timescale, so take the first track's
	// rather than leave an invented value in place. The duration stays zero: a
	// fragmented file does not know its own length, and under HLS the playlist
	// is what states it.
	init.Moov.Mvhd.Timescale = tracks[0].Timescale
	init.Moov.Mvhd.Duration = 0

	buf := bytes.Buffer{}
	if err := init.Encode(&buf); err != nil {
		return nil, fmt.Errorf("remux: encoding init segment: %w", err)
	}
	return buf.Bytes(), nil
}

// MediaSegment builds one HLS media segment: styp, moof and mdat, with samples
// keyed by Track.Number.
//
// baseDecodeTime, also keyed by Track.Number, is where the segment starts on
// the presentation timeline, in that track's timescale. It is written into tfdt
// as given and never derived from the samples, because a player that seeks to
// segment forty has muxed none of the thirty-nine before it: nothing can be
// accumulated, and every segment has to anchor itself to the timeline the
// playlist advertises. Every track carrying samples must appear in the map, and
// Sample.PTS must be measured from the same origin, since the composition
// offsets are the gap between the two.
//
// Samples whose decode times are not yet filled in are completed in place.
func MediaSegment(tracks []Track, samples map[uint64][]Sample, sequence uint32, baseDecodeTime map[uint64]uint64) ([]byte, error) {
	// A track with nothing in this segment gets no traf. An empty one would
	// still carry a tfdt, and a player would believe it.
	carried := make([]int, 0, len(tracks))
	ids := make([]uint32, 0, len(tracks))
	for i, track := range tracks {
		if len(samples[track.Number]) == 0 {
			continue
		}
		carried = append(carried, i)
		ids = append(ids, uint32(i+1))
	}
	if len(carried) == 0 {
		return nil, fmt.Errorf("%w: segment %d carries no samples", ErrUnsupported, sequence)
	}

	frag, err := mp4.CreateMultiTrackFragment(sequence, ids)
	if err != nil {
		return nil, fmt.Errorf("remux: building fragment %d: %w", sequence, err)
	}

	for n, i := range carried {
		track := tracks[i]
		run := samples[track.Number]
		if !hasDecodeTimes(run) {
			AssignDecodeTimes(run)
		}
		base, ok := baseDecodeTime[track.Number]
		if !ok {
			return nil, fmt.Errorf("%w: track %d has no base decode time for segment %d", ErrUnsupported, track.Number, sequence)
		}

		// Composition offsets are measured against where each sample actually
		// lands once the base is imposed, not against Sample.DTS, because the
		// two timelines only coincide when the base happens to equal the first
		// sample's own decode time. Anchoring a B-frame segment at its first
		// frame's presentation time is what makes the later offsets negative,
		// which is why trun is written at version 1.
		decode := base
		durations := sampleDurations(run, track)
		for j, sample := range run {
			// DecodeTime is read only for the first sample of a run, where it
			// becomes the traf's tfdt; the rest accumulate from the durations.
			full := mp4.FullSample{
				Sample: mp4.Sample{
					Flags:                 sampleFlags(track.Kind, sample),
					Dur:                   durations[j],
					Size:                  uint32(len(sample.Data)),
					CompositionTimeOffset: int32(sample.PTS - int64(decode)),
				},
				DecodeTime: base,
				Data:       sample.Data,
			}
			if err := frag.AddFullSampleToTrack(full, ids[n]); err != nil {
				return nil, fmt.Errorf("remux: track %d sample %d: %w", track.Number, j, err)
			}
			decode += uint64(durations[j])
		}
	}

	buf := bytes.Buffer{}
	if err := mp4.CreateStyp().Encode(&buf); err != nil {
		return nil, fmt.Errorf("remux: encoding segment %d: %w", sequence, err)
	}
	if err := frag.Encode(&buf); err != nil {
		return nil, fmt.Errorf("remux: encoding segment %d: %w", sequence, err)
	}
	return buf.Bytes(), nil
}

// sampleDurations gives each sample the time it actually occupies.
//
// A constant derived from the nominal frame rate is the tempting shortcut and
// the wrong one: the error it introduces is small per frame and cumulative, so
// audio and video separate over the length of a film.
//
// The last sample has no successor to measure against, so it falls back to the
// gap the file declares, then to the sample before it.
func sampleDurations(samples []Sample, track Track) []uint32 {
	durations := make([]uint32, len(samples))
	for i, sample := range samples {
		switch {
		case sample.Duration > 0:
			durations[i] = uint32(sample.Duration)
		case i+1 < len(samples):
			durations[i] = uint32(max(samples[i+1].DTS-sample.DTS, 0))
		}
	}

	last := len(samples) - 1
	if durations[last] == 0 {
		durations[last] = nominalDuration(track)
	}
	if durations[last] == 0 && last > 0 {
		durations[last] = durations[last-1]
	}
	return durations
}

// nominalDuration converts Matroska's declared frame gap, which is in
// nanoseconds, into the track's own timescale.
func nominalDuration(track Track) uint32 {
	if track.DefaultDuration <= 0 || track.Timescale == 0 {
		return 0
	}
	return uint32(int64(track.DefaultDuration) * int64(track.Timescale) / int64(time.Second))
}

// sampleFlags marks which samples a player may start decoding at.
//
// Seeking into a segment means starting at its first sync sample, so a stream
// whose sync samples are unmarked is one that cannot be seeked into at all.
//
// Audio is marked sync regardless of what the demuxer said, because every audio
// format carried here decodes frame by frame with no history. A Matroska file
// that leaves the keyframe flag off its audio blocks is common and would
// otherwise produce a segment no player will start at.
func sampleFlags(kind TrackKind, sample Sample) uint32 {
	if kind == TrackAudio || sample.Sync {
		return mp4.SyncSampleFlags
	}
	return mp4.SampleDependsOn1 | mp4.NonSyncSampleFlags
}

// hasDecodeTimes reports whether decode times have already been reconstructed.
//
// Matroska carries none, so a demuxer that has not run AssignDecodeTimes leaves
// every DTS at its zero value. Any non-zero one means the work is done, and a
// run of more than one sample cannot legitimately sit entirely at time zero.
func hasDecodeTimes(samples []Sample) bool {
	for _, sample := range samples {
		if sample.DTS != 0 {
			return true
		}
	}
	return false
}

// mdhdLanguage forces a three-character code. mdhd has room for exactly three,
// and mp4ff answers anything else by adding an elng box, which claims a
// language the file never declared.
func mdhdLanguage(code string) string {
	if len(code) != 3 {
		return "und"
	}
	return code
}

func describeTrack(trak *mp4.TrakBox, track Track, firstFrame []byte) error {
	switch track.Kind {
	case TrackVideo:
		return describeVideo(trak, track)
	case TrackAudio:
		return describeAudio(trak, track, firstFrame)
	default:
		return fmt.Errorf("%w: track is neither video nor audio", ErrUnsupported)
	}
}

func describeVideo(trak *mp4.TrakBox, track Track) error {
	if track.Width == 0 || track.Height == 0 {
		return fmt.Errorf("%w: video track has no frame size", ErrUnsupported)
	}
	if len(track.CodecPrivate) == 0 {
		return fmt.Errorf("%w: video track has no decoder configuration", ErrUnsupported)
	}

	// Matroska's CodecPrivate for both of these is already the decoder
	// configuration record MP4 wants, so avcC and hvcC are that record verbatim
	// and no parameter sets have to be dug out of the frames.
	var config mp4.Box
	switch track.Video {
	case VideoAVC:
		record, err := avc.DecodeAVCDecConfRec(track.CodecPrivate)
		if err != nil {
			return fmt.Errorf("%w: unreadable avcC: %s", ErrUnsupported, err)
		}
		config = &mp4.AvcCBox{DecConfRec: record}
	case VideoHEVC:
		record, err := hevc.DecodeHEVCDecConfRec(track.CodecPrivate)
		if err != nil {
			return fmt.Errorf("%w: unreadable hvcC: %s", ErrUnsupported, err)
		}
		config = &mp4.HvcCBox{DecConfRec: record}
	default:
		return fmt.Errorf("%w: video codec %q", ErrUnsupported, track.Video)
	}

	width, height := uint16(track.Width), uint16(track.Height)
	trak.Tkhd.Width = mp4.Fixed32(uint32(width) << 16)
	trak.Tkhd.Height = mp4.Fixed32(uint32(height) << 16)
	// VideoCodec is the sample entry name, which is where hvc1 rather than hev1
	// is settled.
	entry := mp4.CreateVisualSampleEntryBox(string(track.Video), width, height, config)
	trak.Mdia.Minf.Stbl.Stsd.AddChild(entry)
	return nil
}

func describeAudio(trak *mp4.TrakBox, track Track, firstFrame []byte) error {
	stsd := trak.Mdia.Minf.Stbl.Stsd

	switch track.Audio {
	case AudioAAC:
		if len(track.CodecPrivate) == 0 {
			return fmt.Errorf("%w: AAC track has no AudioSpecificConfig", ErrUnsupported)
		}
		// The AudioSpecificConfig is exactly the decoder-specific info esds
		// wraps, so it is copied in whole.
		esds := mp4.CreateEsdsBox(track.CodecPrivate)
		stsd.AddChild(mp4.CreateAudioSampleEntryBox("mp4a", track.Channels, 16, entrySampleRate(track.SampleRate), esds))
		return nil

	case AudioAC3, AudioEAC3:
		if len(firstFrame) == 0 {
			return fmt.Errorf("%w: %s track has no frame to probe", ErrUnsupported, track.Audio)
		}
		config, err := ParseAC3Syncframe(firstFrame)
		if err != nil {
			return err
		}
		// Channel count and sample rate are taken from the syncframe rather than
		// from Matroska's track header, which describes a downmix often enough
		// to matter.
		if track.Audio == AudioAC3 {
			return trak.SetAC3Descriptor(dac3Box(config))
		}
		return trak.SetEC3Descriptor(dec3Box(config))

	case AudioFLAC:
		config, err := flacConfig(track.CodecPrivate)
		if err != nil {
			return err
		}
		stsd.AddChild(mp4.CreateAudioSampleEntryBox("fLaC", track.Channels, 16, entrySampleRate(track.SampleRate), config))
		return nil

	case AudioALAC:
		if len(track.CodecPrivate) == 0 {
			return fmt.Errorf("%w: ALAC track has no magic cookie", ErrUnsupported)
		}
		stsd.AddChild(mp4.CreateAudioSampleEntryBox("alac", track.Channels, 16, entrySampleRate(track.SampleRate), alacConfig(track.CodecPrivate)))
		return nil

	default:
		return fmt.Errorf("%w: audio codec %q", ErrUnsupported, track.Audio)
	}
}

// entrySampleRate fits a rate into the sample entry's sixteen-bit field, which
// cannot hold 88.2 or 96 kHz. Zero is what players expect when it does not fit:
// the real rate is in the decoder configuration nested inside the entry.
func entrySampleRate(rate uint32) uint16 {
	if rate > 0xFFFF {
		return 0
	}
	return uint16(rate)
}

func dac3Box(config AC3Config) *mp4.Dac3Box {
	return &mp4.Dac3Box{
		FSCod:       config.SampleRateCode,
		BSID:        config.BitstreamID,
		BSMod:       config.BitstreamMode,
		ACMod:       config.ChannelMode,
		LFEOn:       lfeBit(config.LFEOn),
		BitRateCode: config.BitRateCode,
	}
}

// dec3Box describes the one independent substream every E-AC-3 stream has.
// Dependent substreams would add channels beyond the base layout, but the
// syncframe header of the first frame does not name them, and iOS decodes the
// base layout either way.
func dec3Box(config AC3Config) *mp4.Dec3Box {
	return &mp4.Dec3Box{
		EC3Subs: []mp4.EC3Sub{{
			FSCod: config.SampleRateCode,
			BSID:  config.BitstreamID,
			BSMod: config.BitstreamMode,
			ACMod: config.ChannelMode,
			LFEOn: lfeBit(config.LFEOn),
		}},
	}
}

func lfeBit(on bool) byte {
	if on {
		return 1
	}
	return 0
}

// FLAC metadata block types and the fixed size of STREAMINFO, from the FLAC
// format specification.
const (
	flacStreamInfoType = 0
	flacStreamInfoSize = 34
)

// flacConfig turns Matroska's FLAC headers into a dfLa box.
//
// Matroska stores the opening of a FLAC file: the fLaC marker followed by
// metadata blocks. dfLa wants the blocks without the marker, and it wants only
// STREAMINFO, so seek tables and tags are dropped rather than copied into every
// init segment. Some files skip the block framing entirely and store the bare
// 34-byte STREAMINFO payload, which is given a header here.
func flacConfig(codecPrivate []byte) (*mp4.DfLaBox, error) {
	blocks := bytes.TrimPrefix(codecPrivate, []byte("fLaC"))
	if len(blocks) == flacStreamInfoSize {
		return dfLaBox(blocks), nil
	}
	for len(blocks) >= 4 {
		length := int(blocks[1])<<16 | int(blocks[2])<<8 | int(blocks[3])
		if 4+length > len(blocks) {
			break
		}
		if blocks[0]&0x7F == flacStreamInfoType {
			return dfLaBox(blocks[4 : 4+length]), nil
		}
		if blocks[0]&0x80 != 0 {
			break
		}
		blocks = blocks[4+length:]
	}
	return nil, fmt.Errorf("%w: FLAC track has no STREAMINFO", ErrUnsupported)
}

func dfLaBox(streamInfo []byte) *mp4.DfLaBox {
	return &mp4.DfLaBox{MetadataBlocks: []mp4.FLACMetadataBlock{{
		LastMetadataBlockFlag: true,
		BlockType:             flacStreamInfoType,
		Length:                uint32(len(streamInfo)),
		BlockData:             streamInfo,
	}}}
}

// alacConfig wraps ALAC's magic cookie in the alac box the sample entry needs.
// mp4ff has no box for it, so it is built by hand.
//
// What Matroska holds is not consistent: ffmpeg hands the cookie over with the
// box header still attached, while the Matroska spec describes the payload
// alone. An already-wrapped cookie is unwrapped rather than wrapped twice.
func alacConfig(codecPrivate []byte) *mp4.UnknownBox {
	payload := codecPrivate
	if len(payload) > 12 && string(payload[4:8]) == "alac" {
		payload = payload[8:]
	} else {
		payload = append([]byte{0, 0, 0, 0}, payload...) // version and flags
	}
	return mp4.CreateUnknownBox("alac", uint64(8+len(payload)), payload)
}
