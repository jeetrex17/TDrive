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
	WiFiOnly, ChargingOnly              bool
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
	Size                                           int64
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

type JobStatus string

const (
	Pending   JobStatus = "pending"
	Uploading JobStatus = "uploading"
	Complete  JobStatus = "complete"
	Error     JobStatus = "error"
	Paused    JobStatus = "paused"
)

type Status struct {
	Pending, Uploading, Complete, Error, Paused int64
	LastError                                   string
}

type Options struct {
	PageSize    int
	MaxAttempts int
	BaseBackoff time.Duration
	Now         func() time.Time
}
