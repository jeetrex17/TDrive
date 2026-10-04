package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

var errInterruptedDownload = errors.New("simulated process interruption")

type recordingRangeDownloadClient struct {
	tgclient.Client
	ranges     tgclient.RangeClient
	failOffset int64
	failure    error
	mu         sync.Mutex
	read       []int64
}

func (c *recordingRangeDownloadClient) ResolveDocument(ctx context.Context, peer tgclient.InputPeer, msgID int64) (tgclient.DocumentRef, error) {
	return c.ranges.ResolveDocument(ctx, peer, msgID)
}

func (c *recordingRangeDownloadClient) ReadDocumentRange(ctx context.Context, ref tgclient.DocumentRef, offset int64, dst []byte) (int, error) {
	c.mu.Lock()
	c.read = append(c.read, offset)
	c.mu.Unlock()
	if offset == c.failOffset {
		if c.failure != nil {
			return 0, c.failure
		}
		return 0, errInterruptedDownload
	}
	return c.ranges.ReadDocumentRange(ctx, ref, offset, dst)
}

func (c *recordingRangeDownloadClient) saw(offset int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, read := range c.read {
		if read == offset {
			return true
		}
	}
	return false
}

func TestResumableDownloadRestartsWithVerifiedBlocks(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt bool
	}{
		{name: "intact stage"},
		{name: "corrupt checkpoint", corrupt: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const blockSize = 4096
			root := t.TempDir()
			dbPath := filepath.Join(root, "download.db")
			db, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			if err := projection.MigratePersonalChannel(db, personalChannelID); err != nil {
				t.Fatal(err)
			}
			peer := tgclient.InputPeer{ChannelID: personalChannelID, AccessHash: 99}
			fake := tgclient.NewFake(7)
			fake.SeedChannel(peer, "Personal")
			var nextID int64 = 1000
			stageRoot := filepath.Join(root, "staging")
			svc := &Service{DB: db, TG: fake, Peers: testPeerResolver{peer: peer},
				CacheNamespace: "account-7", DownloadStagingDir: stageRoot,
				downloadBlockBytes: blockSize, downloadConcurrency: 1,
				ActorID: func(context.Context) (int64, error) { return 7, nil },
				EmitOp: func(channelID int64, op projection.Op) (int64, error) {
					nextID++
					_, err := projection.ProjectFromOp(db, channelID, nextID, op, 7, projection.Format(op))
					return nextID, err
				},
			}
			body := bigBody(10*blockSize + 77)
			source := writeTempNamedFile(t, "movie.bin", body)
			uploaded, err := svc.Upload(t.Context(), personalChannelID, []string{source}, []string{""}, false)
			if err != nil {
				t.Fatalf("upload: %v", err)
			}
			first := &recordingRangeDownloadClient{Client: fake, ranges: fake, failOffset: 8 * blockSize}
			svc.TG = first
			destination := filepath.Join(root, "movie-out.bin")
			started := svc.StartResumableDownload(t.Context(), personalChannelID,
				uploaded[0].MsgID, uploaded[0].MsgID, func(string) (string, error) { return destination, nil })
			if started.JobID == "" || !errors.Is(started.Err, errInterruptedDownload) {
				t.Fatalf("interrupted download = %+v", started)
			}
			jobs, err := svc.ListResumableDownloads(t.Context())
			if err != nil || len(jobs) != 1 || jobs[0].VerifiedBytes != 8*blockSize {
				t.Fatalf("checkpoint = %+v, err %v", jobs, err)
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("incomplete destination was published: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			stage := filepath.Join(stageRoot, started.JobID+".partial")
			if test.corrupt {
				f, err := os.OpenFile(stage, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.WriteAt([]byte{0xFF}, 0); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			reopened.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = reopened.Close() })
			second := &recordingRangeDownloadClient{Client: fake, ranges: fake, failOffset: -1}
			foreign := &Service{DB: reopened, TG: second, Peers: testPeerResolver{peer: peer},
				CacheNamespace: "another-account", DownloadStagingDir: stageRoot}
			if foreignResult := foreign.ResumeDownload(t.Context(), started.JobID); foreignResult.Status != "error" {
				t.Fatalf("another account resumed job: %+v", foreignResult)
			}
			restarted := &Service{DB: reopened, TG: second, Peers: testPeerResolver{peer: peer},
				CacheNamespace: "account-7", DownloadStagingDir: stageRoot,
				downloadBlockBytes: blockSize, downloadConcurrency: 1}
			result := restarted.ResumeDownload(t.Context(), started.JobID)
			if result.Status != "success" {
				t.Fatalf("resumed download = %+v", result)
			}
			if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, body) {
				t.Fatalf("resumed output mismatch: size %d, err %v", len(got), err)
			}
			if second.saw(0) != test.corrupt || !second.saw(8*blockSize) {
				t.Fatalf("requested offsets = %v, corrupt = %t", second.read, test.corrupt)
			}
			completed, err := restarted.ListResumableDownloads(t.Context())
			if err != nil || len(completed) != 1 || completed[0].Status != downloadCompleted || completed[0].SavedPath != destination {
				t.Fatalf("completed jobs = %+v, err %v", completed, err)
			}
			if !test.corrupt {
				if err := os.Remove(destination); err != nil {
					t.Fatal(err)
				}
				if result := restarted.ResumeDownload(t.Context(), started.JobID); result.Status != "success" {
					t.Fatalf("completed receipt with missing output = %+v", result)
				}
				if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, body) {
					t.Fatalf("recovered missing saved output: size %d, err %v", len(got), err)
				}
			}
			if err := restarted.DiscardResumableDownload(t.Context(), started.JobID); err != nil {
				t.Fatalf("discard completed receipt: %v", err)
			}
			if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, body) {
				t.Fatalf("discard removed saved output: size %d, err %v", len(got), err)
			}
		})
	}
}

func pausedDownloadFixture(t *testing.T) (*Service, *tgclient.Fake, string, string, []byte) {
	t.Helper()
	svc, _, fake, _ := newTestService(t)
	svc.CacheNamespace = "download-fixture"
	svc.DownloadStagingDir = t.TempDir()
	svc.downloadBlockBytes = 4096
	svc.downloadConcurrency = 1
	body := bigBody(10*4096 + 77)
	source := writeTempNamedFile(t, "resume.bin", body)
	files, err := svc.Upload(t.Context(), personalChannelID, []string{source}, []string{""}, false)
	if err != nil {
		t.Fatal(err)
	}
	svc.TG = &recordingRangeDownloadClient{Client: fake, ranges: fake, failOffset: 8 * 4096}
	destination := filepath.Join(t.TempDir(), "resume-out.bin")
	result := svc.StartResumableDownload(t.Context(), personalChannelID, files[0].MsgID, files[0].MsgID,
		func(string) (string, error) { return destination, nil })
	if result.JobID == "" || !errors.Is(result.Err, errInterruptedDownload) {
		t.Fatalf("interrupted download = %+v", result)
	}
	svc.TG = fake
	return svc, fake, result.JobID, destination, body
}

func TestResumableDownloadGuardsSourceAndDestination(t *testing.T) {
	t.Run("source removed", func(t *testing.T) {
		svc, _, jobID, destination, _ := pausedDownloadFixture(t)
		job, err := svc.loadDownloadJob(t.Context(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Delete(t.Context(), personalChannelID, int(job.File.LogicalMsgID)); err != nil {
			t.Fatal(err)
		}
		result := svc.ResumeDownload(t.Context(), jobID)
		if !errors.Is(result.Err, errDownloadSourceChanged) {
			t.Fatalf("changed source resumed: %+v", result)
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("changed source published output: %v", err)
		}
	})
	t.Run("connection engine closed while caller remains live", func(t *testing.T) {
		svc, fake, jobID, _, _ := pausedDownloadFixture(t)
		policy := instantRetryPolicy()
		policy.MaxTransientRetries = 0
		svc.FloodWaitRetry = policy
		svc.TG = &recordingRangeDownloadClient{Client: fake, ranges: fake,
			failOffset: 8 * 4096, failure: fmt.Errorf("engine forcibly closed: %w", context.Canceled)}
		result := svc.ResumeDownload(t.Context(), jobID)
		if result.Status != "error" {
			t.Fatalf("engine-close result = %+v", result)
		}
		job, err := svc.loadDownloadJob(t.Context(), jobID)
		if err != nil || job.Status != downloadWaitingNetwork {
			t.Fatalf("engine-close job = %+v, err %v", job, err)
		}
	})
	t.Run("destination appeared", func(t *testing.T) {
		svc, _, jobID, destination, body := pausedDownloadFixture(t)
		if err := os.WriteFile(destination, []byte("someone else's data"), 0o600); err != nil {
			t.Fatal(err)
		}
		result := svc.ResumeDownload(t.Context(), jobID)
		if result.Status != "error" {
			t.Fatalf("destination conflict = %+v", result)
		}
		job, err := svc.loadDownloadJob(t.Context(), jobID)
		if err != nil || job.Status != downloadNeedsDestination {
			t.Fatalf("conflicted job = %+v, err %v", job, err)
		}
		other := filepath.Join(t.TempDir(), "safe-out.bin")
		if err := svc.ChangeResumableDownloadDestination(t.Context(), jobID, other); err != nil {
			t.Fatal(err)
		}
		if result := svc.ResumeDownload(t.Context(), jobID); result.Status != "success" {
			t.Fatalf("changed destination = %+v", result)
		}
		if got, err := os.ReadFile(destination); err != nil || string(got) != "someone else's data" {
			t.Fatalf("other destination changed: %q, err %v", got, err)
		}
		if got, err := os.ReadFile(other); err != nil || !bytes.Equal(got, body) {
			t.Fatalf("safe destination mismatch: size %d, err %v", len(got), err)
		}
	})
	t.Run("known publish failure remains retryable", func(t *testing.T) {
		svc, _, jobID, destination, body := pausedDownloadFixture(t)
		svc.afterDownloadSaving = func() error { return errInterruptedDownload }
		result := svc.ResumeDownload(t.Context(), jobID)
		if !errors.Is(result.Err, errInterruptedDownload) {
			t.Fatalf("injected publish failure = %+v", result)
		}
		job, err := svc.loadDownloadJob(t.Context(), jobID)
		if err != nil || job.Status != downloadError {
			t.Fatalf("failed publication is not retryable: %+v, err %v", job, err)
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed publication exposed output: %v", err)
		}
		svc.afterDownloadSaving = nil
		if result := svc.ResumeDownload(t.Context(), jobID); result.Status != "success" {
			t.Fatalf("publication retry = %+v", result)
		}
		if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, body) {
			t.Fatalf("publication retry output = %d bytes, err %v", len(got), err)
		}
	})
	t.Run("ambiguous backup needs a new location", func(t *testing.T) {
		svc, _, jobID, destination, _ := pausedDownloadFixture(t)
		if err := os.WriteFile(destination, []byte("unexpected"), 0o600); err != nil {
			t.Fatal(err)
		}
		if result := svc.ResumeDownload(t.Context(), jobID); result.Status != "error" {
			t.Fatalf("destination conflict = %+v", result)
		}
		existing := filepath.Join(t.TempDir(), "existing.bin")
		if err := os.WriteFile(existing, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := svc.ChangeResumableDownloadDestination(t.Context(), jobID, existing); err != nil {
			t.Fatal(err)
		}
		backup := filepath.Join(filepath.Dir(existing), ".tdrive-download-"+jobID+".backup")
		svc.afterDownloadSaving = func() error { return os.WriteFile(backup, []byte("older data"), 0o600) }
		result := svc.ResumeDownload(t.Context(), jobID)
		if result.Status != "error" || !errors.Is(result.Err, errDownloadDestinationChanged) {
			t.Fatalf("ambiguous backup = %+v", result)
		}
		job, err := svc.loadDownloadJob(t.Context(), jobID)
		if err != nil || job.Status != downloadNeedsDestination {
			t.Fatalf("backup conflict job = %+v, err %v", job, err)
		}
		if got, err := os.ReadFile(existing); err != nil || string(got) != "original" {
			t.Fatalf("existing destination changed: %q, err %v", got, err)
		}
		if got, err := os.ReadFile(backup); err != nil || string(got) != "older data" {
			t.Fatalf("ambiguous backup lost: %q, err %v", got, err)
		}
	})
	t.Run("rename receipt survives new owner", func(t *testing.T) {
		svc, fake, jobID, destination, body := pausedDownloadFixture(t)
		if err := os.WriteFile(destination, body, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if _, err := svc.DB.ExecContext(t.Context(), `UPDATE resumable_downloads
			SET status = ?, output_sha256 = ? WHERE job_id = ?`,
			downloadSaving, fmt.Sprintf("%x", sum[:]), jobID); err != nil {
			t.Fatal(err)
		}
		fresh := &Service{DB: svc.DB, TG: fake, Peers: svc.Peers,
			CacheNamespace: svc.CacheNamespace, DownloadStagingDir: svc.DownloadStagingDir}
		result := fresh.ResumeDownload(t.Context(), jobID)
		if result.Status != "success" || result.SavedPath != destination {
			t.Fatalf("reconciled publication = %+v", result)
		}
		job, err := fresh.loadDownloadJob(t.Context(), jobID)
		if err != nil || job.Status != downloadCompleted {
			t.Fatalf("reconciled job = %+v, err %v", job, err)
		}
	})
}

type exactPartDownloadClient struct {
	tgclient.Client
	bodies map[int64][]byte
}

func (c *exactPartDownloadClient) DownloadFileAt(
	ctx context.Context,
	_ tgclient.InputPeer,
	msgID int64,
	dst io.WriterAt,
	baseOffset int64,
	progress func(done, total int64),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body := c.bodies[msgID]
	if _, err := dst.WriteAt(body, baseOffset); err != nil {
		return err
	}
	if progress != nil {
		progress(int64(len(body)), int64(len(body)))
	}
	return nil
}

func TestDownloadMultipartPlainRejectsPartSizeMismatch(t *testing.T) {
	tests := []struct {
		name   string
		bodies map[int64][]byte
	}{
		{
			name: "short non-final part",
			bodies: map[int64][]byte{
				101: []byte("ab"),
				102: []byte("WXYZ"),
			},
		},
		{
			name: "oversized non-final part",
			bodies: map[int64][]byte{
				101: []byte("abcdef"),
				102: []byte("WXYZ"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			destination := filepath.Join(directory, "archive.bin")
			service := &Service{TG: &exactPartDownloadClient{bodies: test.bodies}}
			file := projection.DownloadFile{
				LogicalMsgID: 200,
				Name:         "archive.bin",
				UploadUUID:   "parts",
				PartCount:    2,
				StoredSize:   8,
				OutputSize:   8,
				Parts: []projection.FilePart{
					{PartIndex: 0, MsgID: 101, Size: 4},
					{PartIndex: 1, MsgID: 102, Size: 4},
				},
			}

			err := service.downloadProjectedFileToPath(
				context.Background(), tgclient.InputPeer{}, file, destination, nil, nil,
			)
			if err == nil {
				t.Fatal("download succeeded with mismatched part body")
			}
			if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
				t.Fatalf("destination was published after verification failure: %v", statErr)
			}
			matches, globErr := filepath.Glob(filepath.Join(directory, ".tdrive-download-*"))
			if globErr != nil {
				t.Fatalf("glob temporary downloads: %v", globErr)
			}
			if len(matches) != 0 {
				t.Fatalf("temporary downloads retained after failure: %v", matches)
			}
		})
	}
}

func TestDownloadMultipartPlainStreamsVerifiedParts(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "archive.bin")
	service := &Service{TG: &exactPartDownloadClient{bodies: map[int64][]byte{
		101: []byte("abcd"),
		102: []byte("WXYZ"),
	}}}
	file := projection.DownloadFile{
		LogicalMsgID: 200,
		Name:         "archive.bin",
		UploadUUID:   "parts",
		PartCount:    2,
		StoredSize:   8,
		OutputSize:   8,
		Parts: []projection.FilePart{
			{PartIndex: 0, MsgID: 101, Size: 4},
			{PartIndex: 1, MsgID: 102, Size: 4},
		},
	}

	if err := service.downloadProjectedFileToPath(
		context.Background(), tgclient.InputPeer{}, file, destination, nil, nil,
	); err != nil {
		t.Fatalf("download multipart file: %v", err)
	}
	body, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if got, want := string(body), "abcdWXYZ"; got != want {
		t.Fatalf("download body = %q, want %q", got, want)
	}
}
