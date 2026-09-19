package remux

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// HLS is the delivery format because a progressive fragmented MP4 cannot work
// here, not because it is nicer.
//
// A player seeks a progressive file by byte offset, which it resolves from an
// index listing every fragment's compressed size. Those sizes are unknowable
// until the whole file has been muxed, so publishing that index would mean
// pulling the entire source from Telegram before the first frame. HLS asks for
// no such thing: the player finds a segment by adding up the durations below,
// and each one maps to a single range read.

// InitName and playlist entries are relative URIs, resolved by the player
// against the playlist's own URL. Keeping them relative means the server can
// mount the playlist anywhere without rewriting it.
const InitName = "init.mp4"

// MediaName is the media playlist inside one rendition's folder.
const MediaName = "index.m3u8"

// Renditions are served as separate streams rather than muxed together,
// because a file with two audio tracks cannot offer a choice otherwise: HLS
// switches audio by switching which rendition is playing, and a track welded
// into the video segments can never be switched away from.
//
// The cost of separating them is nothing here. Both renditions of a segment
// come from the same stretch of the source, so the second read is served from
// the block cache the first one filled.
const VideoRendition = "v"

// AudioRendition names the rendition carrying one audio track.
func AudioRendition(index int) string {
	return fmt.Sprintf("a%d", index)
}

// ParseRendition recovers what a request is asking for, refusing anything this
// server does not publish. Like ParseSegmentName it runs on untrusted paths, so
// it refuses rather than guesses.
func ParseRendition(name string) (audio int, isAudio bool, ok bool) {
	if name == VideoRendition {
		return 0, false, true
	}
	rest, cut := strings.CutPrefix(name, "a")
	if !cut || rest == "" || (len(rest) > 1 && rest[0] == '0') {
		return 0, false, false
	}
	index, err := strconv.Atoi(rest)
	if err != nil || index < 0 || index > 1<<10 {
		return 0, false, false
	}
	return index, true, true
}

// SegmentName is the URI of one media segment.
func SegmentName(index int) string {
	return fmt.Sprintf("%d.m4s", index)
}

// Playlist renders the VOD media playlist for a plan.
//
// Version 7 is the floor for fragmented MP4 segments, which also makes
// EXT-X-MAP mandatory: without it the player has no initialisation segment and
// cannot decode anything.
func Playlist(segments []Segment) string {
	var out strings.Builder
	out.WriteString("#EXTM3U\n")
	out.WriteString("#EXT-X-VERSION:7\n")
	fmt.Fprintf(&out, "#EXT-X-TARGETDURATION:%d\n", targetDuration(segments))
	out.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	out.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	fmt.Fprintf(&out, "#EXT-X-MAP:URI=%q\n", InitName)

	for _, segment := range segments {
		fmt.Fprintf(&out, "#EXTINF:%.3f,\n", segment.Duration.Seconds())
		out.WriteString(SegmentName(segment.Index) + "\n")
	}

	// Without this the player treats the presentation as live and will not let
	// anyone seek past what has been loaded.
	out.WriteString("#EXT-X-ENDLIST\n")
	return out.String()
}

// targetDuration is the advertised ceiling on segment length.
//
// The spec requires it to be an integer no smaller than any segment, and a
// player that finds a longer segment than advertised may stall. Rounding up
// rather than to nearest is therefore the only safe direction.
func targetDuration(segments []Segment) int {
	longest := 0.0
	for _, segment := range segments {
		if seconds := segment.Duration.Seconds(); seconds > longest {
			longest = seconds
		}
	}
	if longest <= 0 {
		return 1
	}
	return int(math.Ceil(longest))
}

// MasterPlaylist ties the video to the audio tracks on offer.
//
// The audio tracks become EXT-X-MEDIA renditions of one group, and the video
// variant names that group, which is what puts a switchable list in front of
// the viewer. The first playable track is the default because a file's track
// order is the only statement of intent available: Matroska's default flag
// routinely points at a format this platform cannot decode, which is why
// BestAudio ignores it.
//
// bandwidth is the stream's average bits per second. It is required on a
// variant, and a player uses it to decide whether the connection can carry the
// stream at all.
func MasterPlaylist(video Track, audio []Track, bandwidth int) string {
	var out strings.Builder
	out.WriteString("#EXTM3U\n")
	out.WriteString("#EXT-X-VERSION:7\n")
	// Every segment opens on a keyframe, which is what lets a player start at
	// any of them rather than only at the first.
	out.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")

	for i, track := range audio {
		fmt.Fprintf(&out, "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=%q,NAME=%q", audioGroup, audioName(track, i))
		if track.Language != "" && track.Language != "und" {
			fmt.Fprintf(&out, ",LANGUAGE=%q", track.Language)
		}
		fmt.Fprintf(&out, ",DEFAULT=%s,AUTOSELECT=YES,URI=%q\n", yesNo(i == 0), AudioRendition(i)+"/"+MediaName)
	}

	fmt.Fprintf(&out, "#EXT-X-STREAM-INF:BANDWIDTH=%d", bandwidth)
	if codecs := variantCodecs(video, audio); codecs != "" {
		fmt.Fprintf(&out, ",CODECS=%q", codecs)
	}
	if video.Width > 0 && video.Height > 0 {
		fmt.Fprintf(&out, ",RESOLUTION=%dx%d", video.Width, video.Height)
	}
	if len(audio) > 0 {
		fmt.Fprintf(&out, ",AUDIO=%q", audioGroup)
	}
	out.WriteString("\n" + VideoRendition + "/" + MediaName + "\n")
	return out.String()
}

const audioGroup = "audio"

// variantCodecs describes the video plus the audio a player would start with,
// which is the default rendition. The alternates are the same codec often
// enough, and a variant lists what it plays rather than everything available.
func variantCodecs(video Track, audio []Track) string {
	tracks := []Track{video}
	if len(audio) > 0 {
		tracks = append(tracks, audio[0])
	}
	return CodecsAttribute(tracks...)
}

// audioName is what the viewer picks from, so it has to say something. A file
// that names its tracks is believed; otherwise the language is the next most
// useful thing, and a bare number is the last resort.
func audioName(track Track, index int) string {
	if track.Name != "" {
		return track.Name
	}
	if track.Language != "" && track.Language != "und" {
		return strings.ToUpper(track.Language)
	}
	return fmt.Sprintf("Audio %d", index+1)
}

func yesNo(value bool) string {
	if value {
		return "YES"
	}
	return "NO"
}

// PlaylistContentType is what the playlist must be served as. AVFoundation
// loads HLS out of process and decides what it has from this header, so a
// generic type means the file is fetched and then ignored.
const PlaylistContentType = "application/vnd.apple.mpegurl"

// SegmentContentType is the media segment type.
const SegmentContentType = "video/iso.segment"

// InitContentType is the initialisation segment type.
const InitContentType = "video/mp4"

// ParseSegmentName recovers a segment index from its URI, rejecting anything
// that is not one of the names SegmentName produces.
//
// The server routes untrusted paths through here, so it refuses rather than
// guesses: a permissive parse is how a path becomes a way to read other files.
func ParseSegmentName(name string) (int, bool) {
	rest, ok := strings.CutSuffix(name, ".m4s")
	if !ok || rest == "" {
		return 0, false
	}
	index := 0
	for _, digit := range rest {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		index = index*10 + int(digit-'0')
		if index > 1<<20 {
			return 0, false
		}
	}
	// A leading zero would let one segment be addressed by several names, which
	// defeats caching and invites confusion.
	if len(rest) > 1 && rest[0] == '0' {
		return 0, false
	}
	return index, true
}
