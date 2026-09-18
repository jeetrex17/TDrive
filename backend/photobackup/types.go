package photobackup

import (
	"context"
	"errors"
	"time"
)

var ErrInvalid = errors.New("photobackup: invalid argument")
var ErrCursorExpired = errors.New("photobackup: cursor expired")

type Scope struct {
	AccountID string
	DriveID   int64
}

func (s Scope) valid() bool { return s.AccountID != "" && s.DriveID > 0 }

type Settings struct {
	Scope                               Scope
	Enabled, Photos, Videos, FutureOnly bool
	WiFiOnly                            bool
	ManualPaused                        bool
	DestinationParentID                 string
	Encrypt                             bool
}

type Source struct {
	Scope                Scope
	ID, Kind, Root, Name string
	Enabled              bool
	AddedAt              time.Time
}

type Asset struct {
	ID, Version, Path, Name, MediaType, ResourceID string
	ModifiedAt                                     time.Time
	// CapturedAt is when the camera took the picture, if the host knows; zero
	// otherwise. ModifiedAt is the file's own timestamp and moves whenever a
	// photo is edited, which is why "new items only" cannot key off it alone.
	CapturedAt time.Time
	Size       int64
}

type Page struct {
	Assets     []Asset
	NextCursor string
}
type Adapter interface {
	Page(context.Context, Source, string, int) (Page, error)
}

type UploadRequest struct {
	Scope     Scope
	Source    Source
	Asset     Asset
	ChannelID int64
	ParentID  string
	Encrypt   bool
}
type UploadResult struct{ RemoteMessageID int64 }
type Uploader func(context.Context, UploadRequest) (UploadResult, error)

// RemoteIndex answers, for one bounded batch of remote message ids taken from
// completed receipts, which of them the drive can no longer produce a file for.
//
// Returning the absent ids rather than the present ones makes the safe answer
// the cheap one: an implementation that cannot decide returns nothing and the
// ledger is left exactly as it was. An id must only be reported when the drive
// is known to have lost it for good -- an unfinished index, or a delete the
// user can still undo, is not a loss.
type RemoteIndex func(ctx context.Context, driveID int64, messageIDs []int64) ([]int64, error)

type JobStatus string

const (
	Pending   JobStatus = "pending"
	Uploading JobStatus = "uploading"
	Complete  JobStatus = "complete"
	Error     JobStatus = "error"
	Paused    JobStatus = "paused"
	// Missing is a receipt the drive no longer honours: the upload really
	// happened, and the file it produced is gone. It is a resting state, never
	// a queue -- the work only resumes when the user explicitly retries.
	Missing JobStatus = "missing"
)

type Status struct {
	Pending, Uploading, Complete, Error, Paused, Missing int64
	LastError                                            string
}

type Options struct {
	PageSize    int
	MaxAttempts int
	BaseBackoff time.Duration
	Now         func() time.Time
}
