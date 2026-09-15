package media

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

// atom builds one MP4 box header followed by payload bytes.
func atom(kind string, payload int) []byte {
	box := make([]byte, 8+payload)
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], kind)
	return box
}

// largeAtom builds a box that uses the 64-bit size form.
func largeAtom(kind string, payload int) []byte {
	box := make([]byte, 16+payload)
	binary.BigEndian.PutUint32(box[:4], 1)
	copy(box[4:8], kind)
	binary.BigEndian.PutUint64(box[8:16], uint64(len(box)))
	return box
}

func sliceReader(file []byte) func(off int64, n int) ([]byte, error) {
	return func(off int64, n int) ([]byte, error) {
		if off < 0 || off+int64(n) > int64(len(file)) {
			return nil, errors.New("read past end")
		}
		return file[off : off+int64(n)], nil
	}
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func TestMP4IndexOffsetFindsIndexAfterMediaData(t *testing.T) {
	ftyp, free, mdat, moov := atom("ftyp", 16), atom("free", 0), atom("mdat", 1000), atom("moov", 300)
	file := concat(ftyp, free, mdat, moov)

	got, ok := mp4IndexOffset(sliceReader(file), int64(len(file)))
	if !ok || got != int64(len(ftyp)+len(free)+len(mdat)) {
		t.Fatalf("index = %d, %t; want the moov offset after mdat", got, ok)
	}

	wide := concat(ftyp, largeAtom("mdat", 2000), moov)
	got, ok = mp4IndexOffset(sliceReader(wide), int64(len(wide)))
	if !ok || got != int64(len(ftyp)+16+2000) {
		t.Fatalf("index after 64-bit mdat = %d, %t", got, ok)
	}
}

func TestMP4IndexOffsetIgnoresFilesWithNothingToWarm(t *testing.T) {
	ftyp, moov, mdat := atom("ftyp", 16), atom("moov", 300), atom("mdat", 1000)
	cases := map[string][]byte{
		"index up front":       concat(ftyp, moov, mdat),
		"mdat runs to the end": concat(ftyp, func() []byte { b := atom("mdat", 40); binary.BigEndian.PutUint32(b[:4], 0); return b }()),
		"mdat is the last box": concat(ftyp, mdat),
		"corrupt box size":     concat(ftyp, func() []byte { b := atom("free", 8); binary.BigEndian.PutUint32(b[:4], 3); return b }(), mdat, moov),
		"not an mp4 at all":    []byte("<!doctype html><html><body>not a video</body></html>"),
	}
	for name, file := range cases {
		if got, ok := mp4IndexOffset(sliceReader(file), int64(len(file))); ok {
			t.Fatalf("%s: index = %d, want none", name, got)
		}
	}
	if _, ok := mp4IndexOffset(func(int64, int) ([]byte, error) { return nil, errors.New("offline") }, 1<<20); ok {
		t.Fatal("a failed read produced an index")
	}
}

// Opening an MP4 with its index after the media data warms that index behind
// the head, following the stored bytes across multipart segments.
func TestNewSessionWarmsMP4IndexAcrossSegments(t *testing.T) {
	block := int64(tgclient.RangeReadMaxBytes)
	ftyp := atom("ftyp", 16)
	mdatPayload := int(3*block) - len(ftyp) - 8 + 4096
	file := concat(ftyp, atom("mdat", mdatPayload), atom("moov", int(block)))
	size := int64(len(file))
	cut := 2 * block
	first := tgclient.DocumentRef{Peer: tgclient.InputPeer{ChannelID: 1}, MsgID: 1, Size: cut}
	second := tgclient.DocumentRef{Peer: tgclient.InputPeer{ChannelID: 1}, MsgID: 2, Size: size - cut}
	ranges := &segmentedRangeFake{bodies: map[int64][]byte{1: file[:cut], 2: file[cut:]}, reads: make(chan rangeCall, 32)}
	logical := LogicalFile{ChannelID: 1, FileID: 3, Name: "movie.mp4", StoredSize: size, PlaintextSize: size}
	segments := []resolvedSegment{{start: 0, size: cut, ref: first}, {start: cut, size: size - cut, ref: second}}

	session, err := newSession(logical, segments, ranges, nil, nil, SessionOptions{})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	t.Cleanup(session.Close)

	// The index starts 4 KiB into block 3, which is block 1 of the second
	// segment; warming covers it and the block after it, where the file ends.
	want := map[rangeCall]bool{
		{offset: block, length: int(block)}:                    true,
		{offset: 2 * block, length: int(size - cut - 2*block)}: true,
	}
	deadline := time.After(2 * time.Second)
	for len(want) > 0 {
		select {
		case call := <-ranges.reads:
			delete(want, call)
		case <-deadline:
			t.Fatalf("index blocks never warmed, still waiting for %v", want)
		}
	}
}

// segmentedRangeFake serves several documents and reports every read.
type segmentedRangeFake struct {
	bodies map[int64][]byte
	reads  chan rangeCall
}

func (f *segmentedRangeFake) ResolveDocument(context.Context, tgclient.InputPeer, int64) (tgclient.DocumentRef, error) {
	return tgclient.DocumentRef{}, errors.New("not used")
}

func (f *segmentedRangeFake) ReadDocumentRange(_ context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	body, ok := f.bodies[ref.MsgID]
	if !ok || offset+int64(len(dst)) > int64(len(body)) {
		return 0, errors.New("read past end")
	}
	f.reads <- rangeCall{offset: offset, length: len(dst)}
	return copy(dst, body[offset:offset+int64(len(dst))]), nil
}
