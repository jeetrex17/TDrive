package media

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"TDrive/backend/projection"
)

// hlsFixture encodes a short Matroska file and returns its bytes, so the tests
// below exercise the real demuxer rather than a stand-in.
func hlsFixture(t *testing.T, args ...string) []byte {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}

	path := filepath.Join(t.TempDir(), "fixture.mkv")
	args = append([]string{
		"-y", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=12",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=12:sample_rate=48000",
	}, append(args, path)...)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg could not build the fixture: %v\n%s", err, out)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func playableMKV(t *testing.T) []byte {
	return hlsFixture(t,
		"-c:v", "libx264", "-preset", "ultrafast",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-bf", "2",
		"-c:a", "aac", "-b:a", "64k")
}

// openFileSession stands a whole media service up over one file's bytes and
// returns what the frontend would receive.
func openFileSession(t *testing.T, name string, body []byte) OpenResult {
	t.Helper()

	db := newResolverTestDB(t)
	mustApplyOp(t, db, 10, projection.Op{
		Type:     projection.OpFileUpload,
		Parent:   projection.RootParent,
		Name:     name,
		FileSize: int64(len(body)),
	})
	ranges := newMediaRangeFake(map[int64][]byte{10: body})
	svc := NewService(Config{
		DB:     db,
		Peers:  staticPeerResolver{peer: ranges.peer},
		Ranges: ranges,
	})
	t.Cleanup(func() { _ = svc.Close() })

	opened, err := svc.Open(context.Background(), testChannelID, 10)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return opened
}

func get(t *testing.T, url string) (int, string, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

func TestHLSIsOfferedOnlyForContainersThatNeedIt(t *testing.T) {
	// An MP4 plays as it is everywhere, so offering a remux of it would add a
	// repackaging step and lose byte-range seeking for nothing.
	if opened := openFileSession(t, "clip.mp4", testBytes(2048)); opened.HLSURL != "" {
		t.Errorf("MP4 was offered a remux at %q", opened.HLSURL)
	}
	if opened := openFileSession(t, "movie.mkv", testBytes(2048)); opened.HLSURL == "" {
		t.Error("MKV was not offered a remux")
	}
}

func TestHLSServesAStreamAPlayerCanFollow(t *testing.T) {
	// The strongest test available: hand a real HLS client the same URL the app
	// hands the phone, and let it follow the master to the variant, the variant
	// to the segments, and the audio group to the rendition it wants. Every
	// route, every playlist and every segment has to be right for this to
	// produce a frame.
	opened := openFileSession(t, "movie.mkv", playableMKV(t))

	status, contentType, master := get(t, opened.HLSURL)
	if status != http.StatusOK {
		t.Fatalf("master status = %d, want 200\n%s", status, master)
	}
	// AVFoundation loads HLS out of process and decides what it has from this
	// header, so a generic type means the playlist is fetched and then ignored.
	if contentType != "application/vnd.apple.mpegurl" {
		t.Errorf("master served as %q", contentType)
	}
	for _, required := range []string{"#EXT-X-STREAM-INF:", "#EXT-X-MEDIA:TYPE=AUDIO", "v/index.m3u8"} {
		if !strings.Contains(string(master), required) {
			t.Errorf("master is missing %s\n%s", required, master)
		}
	}

	played := filepath.Join(t.TempDir(), "played.mp4")
	out, err := exec.Command("ffmpeg", "-y", "-v", "error",
		"-i", opened.HLSURL, "-map", "0:v", "-map", "0:a", "-c", "copy", played).CombinedOutput()
	if err != nil {
		t.Fatalf("a player could not follow the stream: %v\n%s", err, out)
	}
	if len(out) > 0 {
		t.Errorf("the client complained while following the stream:\n%s", out)
	}

	decoded, err := exec.Command("ffmpeg", "-v", "error", "-i", played, "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("what the routes served does not decode: %v\n%s", err, decoded)
	}
	if len(decoded) > 0 {
		t.Errorf("decoder complained about what the routes served:\n%s", decoded)
	}
}

func TestHLSOffersEveryAudioTrackTheFileCarries(t *testing.T) {
	// A film with a commentary track is the case this exists for: HLS switches
	// audio by switching rendition, so a track that is not published as one can
	// never be reached.
	body := hlsFixture(t,
		"-f", "lavfi", "-i", "sine=frequency=880:duration=12:sample_rate=48000",
		"-map", "0:v", "-map", "1:a", "-map", "2:a",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:0", "title=Surround",
		"-metadata:s:a:1", "language=fra", "-metadata:s:a:1", "title=Commentary",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
		"-c:a", "aac", "-b:a", "64k")
	opened := openFileSession(t, "movie.mkv", body)

	_, _, master := get(t, opened.HLSURL)
	renditions := strings.Count(string(master), "#EXT-X-MEDIA:TYPE=AUDIO")
	if renditions != 2 {
		t.Fatalf("master publishes %d audio renditions, want 2\n%s", renditions, master)
	}
	for _, expected := range []string{`NAME="Surround"`, `NAME="Commentary"`, `LANGUAGE="fra"`, `DEFAULT=YES`, `DEFAULT=NO`} {
		if !strings.Contains(string(master), expected) {
			t.Errorf("master is missing %s\n%s", expected, master)
		}
	}

	// The second rendition has to be reachable and playable on its own, or the
	// menu entry is a promise the server cannot keep.
	base := strings.TrimSuffix(opened.HLSURL, hlsPlaylistName)
	if status, _, _ := get(t, base+"a1/index.m3u8"); status != http.StatusOK {
		t.Errorf("the second audio rendition answered %d", status)
	}
	if status, _, _ := get(t, base+"a1/init.mp4"); status != http.StatusOK {
		t.Errorf("the second audio rendition has no initialisation segment")
	}
	if status, _, body := get(t, base+"a1/0.m4s"); status != http.StatusOK || len(body) == 0 {
		t.Errorf("the second audio rendition served nothing: status %d, %d bytes", status, len(body))
	}
}

func TestHLSSaysWhenAFileCannotBeRepackaged(t *testing.T) {
	// 415 rather than 500: this file will never play here however many times it
	// is asked for, and the player shows a dead end differently from a retry.
	opened := openFileSession(t, "movie.mkv", hlsFixture(t,
		"-c:v", "mpeg4", "-q:v", "5", "-c:a", "mp2", "-b:a", "128k"))

	status, _, body := get(t, opened.HLSURL)
	if status != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415\n%s", status, body)
	}
}

func TestHLSRefusesPathsItDidNotPublish(t *testing.T) {
	opened := openFileSession(t, "movie.mkv", playableMKV(t))
	base := strings.TrimSuffix(opened.HLSURL, "index.m3u8")

	// A segment index past the plan, a name that is not one this server
	// produces, a rendition that does not exist, and a traversal attempt all
	// have to be refused rather than guessed at: this is an untrusted path.
	for _, name := range []string{
		"v/9999.m4s", "v/01.m4s", "v/index.m3u", "v/../../etc/passwd",
		"a9/0.m4s", "a01/index.m3u8", "x/init.mp4", "0.m4s",
	} {
		if status, _, _ := get(t, base+name); status != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", name, status)
		}
	}

	badToken := strings.Replace(opened.HLSURL, opened.Token, "not-a-token", 1)
	if status, _, _ := get(t, badToken); status != http.StatusNotFound {
		t.Errorf("bad token status = %d, want 404", status)
	}
}
