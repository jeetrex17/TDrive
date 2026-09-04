package file

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

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
