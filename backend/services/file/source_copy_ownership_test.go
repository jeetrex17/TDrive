package file

import (
	"slices"
	"testing"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

func TestSourceCopyCleanupPreservesReferencedBodies(t *testing.T) {
	for _, test := range []struct {
		name      string
		useUUID   bool
		sameDrive bool
	}{
		{name: "multipart revision owns upload", useUUID: true, sameDrive: true},
		{name: "single-part revision owns second body", sameDrive: true},
		{name: "other drive can use the same message ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, telegram, _ := newSourceCopyFixture(t)
			job := sourceCopyJob{
				Result: SourceCopyResult{ID: projection.NewUploadUUID()}, Source: sourceCopyTestInput().Source,
				ChannelID: personalChannelID,
				Parts: []sourceCopyPart{
					{DocumentID: 21, Size: 12, BodyID: 91},
					{DocumentID: 22, Size: 12, BodyID: 92},
				},
			}
			for index, part := range job.Parts {
				telegram.SeedHistory(tgclient.HistoryMessage{
					ChannelID: job.ChannelID, MsgID: part.BodyID, FromID: job.Source.AccountID,
					Text: job.bodyHeader(index), HasMedia: true, DocumentID: part.DocumentID, MediaSize: part.Size,
				})
			}
			revisionDrive := int64(sharedChannelID)
			if test.sameDrive {
				revisionDrive = job.ChannelID
			}
			contentID, uploadUUID := job.Parts[1].BodyID, ""
			if test.useUUID {
				contentID, uploadUUID = 0, job.bodyUUID()
			}
			ctx := t.Context()
			if _, err := svc.DB.ExecContext(ctx, `
				INSERT INTO file_revisions(channel_id, file_msg_id, revision, committed_msg_id, content_msg_id, upload_uuid)
				VALUES (?, 93, 1, 93, ?, ?)
			`, revisionDrive, contentID, uploadUUID); err != nil {
				t.Fatal(err)
			}

			worker := sourceCopyWorker{Service: svc, peer: tgclient.InputPeer{ChannelID: job.ChannelID}}
			bodies, err := worker.cleanableBodies(ctx, job)
			if test.sameDrive {
				if err == nil || len(bodies) != 0 {
					t.Fatalf("referenced destination bodies = %v, %v", bodies, err)
				}
			} else if err != nil || !slices.Equal(bodies, []int64{91, 92}) {
				t.Fatalf("unreferenced destination bodies = %v, %v", bodies, err)
			}
		})
	}
}
