package mountadapter

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"TDrive/backend/mountwrite"
	"TDrive/backend/projection"
	fileservice "TDrive/backend/services/file"
	"TDrive/backend/tgclient"
)

const (
	defaultRevisionRetention = 30 * 24 * time.Hour
	defaultHistoryPageSize   = 100
	defaultHistoryPages      = 3
)

type HiddenStore interface {
	UploadHidden(context.Context, int64, fileservice.HiddenUploadRequest, io.ReadSeeker) (fileservice.HiddenBody, error)
	RecoverHiddenUpload(context.Context, int64, fileservice.HiddenUploadRequest, io.ReadSeeker) (fileservice.HiddenBody, error)
	DiscardHiddenReceipt(context.Context, int64, string, fileservice.HiddenBody) error
	DiscardHiddenOperation(context.Context, int64, string) error
}

type TelegramRemoteConfig struct {
	DB              *sql.DB
	DriveID         int64
	Files           HiddenStore
	Telegram        tgclient.Client
	Peers           fileservice.PeerResolver
	ActorID         func(context.Context) (int64, error)
	Now             func() time.Time
	HistoryPageSize int
	HistoryPages    int
	FloodWaitRetry  tgclient.FloodWaitRetryPolicy
}

type TelegramRemote struct {
	db              *sql.DB
	driveID         int64
	files           HiddenStore
	telegram        tgclient.Client
	peers           fileservice.PeerResolver
	actorID         func(context.Context) (int64, error)
	now             func() time.Time
	historyPageSize int
	historyPages    int
	floodWaitRetry  tgclient.FloodWaitRetryPolicy
}

func NewTelegramRemote(config TelegramRemoteConfig) (*TelegramRemote, error) {
	if config.DB == nil || config.DriveID <= 0 || config.Files == nil || config.Telegram == nil || config.Peers == nil || config.ActorID == nil {
		return nil, mountwrite.ErrInvalidRequest
	}
	if _, ok := config.Telegram.(tgclient.IdempotentSender); !ok {
		return nil, mountwrite.ErrInvalidRequest
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.HistoryPageSize <= 0 {
		config.HistoryPageSize = defaultHistoryPageSize
	}
	if config.HistoryPages <= 0 {
		config.HistoryPages = defaultHistoryPages
	}
	if isZeroFloodWaitRetryPolicy(config.FloodWaitRetry) {
		config.FloodWaitRetry = tgclient.DefaultWriteFloodWaitRetryPolicy()
	}
	if err := validateFloodWaitRetryPolicy(config.FloodWaitRetry); err != nil {
		return nil, mountwrite.ErrInvalidRequest
	}
	return &TelegramRemote{
		db:              config.DB,
		driveID:         config.DriveID,
		files:           config.Files,
		telegram:        config.Telegram,
		peers:           config.Peers,
		actorID:         config.ActorID,
		now:             config.Now,
		historyPageSize: config.HistoryPageSize,
		historyPages:    config.HistoryPages,
		floodWaitRetry:  config.FloodWaitRetry,
	}, nil
}

func (remote *TelegramRemote) UploadHidden(ctx context.Context, request mountwrite.HiddenUpload, source io.ReadSeeker) (mountwrite.RemoteBody, error) {
	if remote == nil || source == nil || request.DriveID != remote.driveID {
		return mountwrite.RemoteBody{}, mountwrite.ErrInvalidRequest
	}
	body, err := remote.files.UploadHidden(ctx, request.DriveID, hiddenUploadRequest(request), source)
	mapped := remoteBodyFromHidden(request, body)
	if err != nil {
		if errors.Is(err, tgclient.ErrSendOutcomeUnknown) || errors.Is(err, fileservice.ErrHiddenReceiptRecoveryRequired) {
			return mapped, errors.Join(mountwrite.ErrUploadOutcomeUnknown, err)
		}
		return mapped, err
	}
	return mapped, nil
}

func (remote *TelegramRemote) RecoverHidden(ctx context.Context, request mountwrite.HiddenUpload, source io.ReadSeeker) (mountwrite.RemoteBody, error) {
	if remote == nil || source == nil || request.DriveID != remote.driveID {
		return mountwrite.RemoteBody{}, mountwrite.ErrInvalidRequest
	}
	body, err := remote.files.RecoverHiddenUpload(ctx, request.DriveID, hiddenUploadRequest(request), source)
	if err != nil {
		return mountwrite.RemoteBody{}, err
	}
	return remoteBodyFromHidden(request, body), nil
}

func hiddenUploadRequest(request mountwrite.HiddenUpload) fileservice.HiddenUploadRequest {
	return fileservice.HiddenUploadRequest{
		OperationID:   request.OperationID,
		Name:          request.Name,
		StoredSize:    request.StoredSize,
		PlaintextSize: request.PlaintextSize,
		Encrypted:     request.Encrypted,
	}
}

func remoteBodyFromHidden(request mountwrite.HiddenUpload, body fileservice.HiddenBody) mountwrite.RemoteBody {
	return mountwrite.RemoteBody{
		UploadUUID:        body.UploadUUID,
		PartCount:         body.PartCount,
		PlaintextSize:     body.PlaintextSize,
		StoredSize:        body.StoredSize,
		Encrypted:         body.Encrypted,
		EncryptionVersion: request.EncryptionVersion,
		SHA256:            request.SHA256,
		StoredSHA256:      request.StoredSHA256,
		MessageIDs:        append([]int64(nil), body.MessageIDs...),
	}
}

func (remote *TelegramRemote) DiscardHidden(ctx context.Context, operationID string, body *mountwrite.RemoteBody) error {
	if remote == nil {
		return mountwrite.ErrInvalidRequest
	}
	if body == nil {
		return remote.files.DiscardHiddenOperation(ctx, remote.driveID, operationID)
	}
	return remote.files.DiscardHiddenReceipt(ctx, remote.driveID, operationID, fileservice.HiddenBody{
		UploadUUID:    body.UploadUUID,
		PartCount:     body.PartCount,
		StoredSize:    body.StoredSize,
		PlaintextSize: body.PlaintextSize,
		Encrypted:     body.Encrypted,
		MessageIDs:    append([]int64(nil), body.MessageIDs...),
	})
}

func (remote *TelegramRemote) Commit(ctx context.Context, request mountwrite.CommitRequest) (mountwrite.MutationResult, error) {
	if remote == nil || request.Mutation.DriveID != remote.driveID {
		return mountwrite.MutationResult{}, mountwrite.ErrInvalidRequest
	}
	now := request.CommitTime.UTC()
	if now.IsZero() {
		now = remote.now().UTC()
	}
	op, err := buildProjectionOperation(request, now)
	if err != nil {
		return mountwrite.MutationResult{}, err
	}
	peer, err := remote.peers.ResolvePeer(ctx, request.Mutation.DriveID)
	if err != nil {
		return mountwrite.MutationResult{}, err
	}
	randomID, err := tgclient.StableRandomID(request.OperationID, "commit")
	if err != nil {
		return mountwrite.MutationResult{}, err
	}
	header := projection.Format(op)
	var msgID int64
	err = remote.floodWaitRetry.Do(ctx, func() error {
		var sendErr error
		msgID, sendErr = tgclient.SendControlIdempotent(ctx, remote.telegram, peer, header, true, randomID)
		return sendErr
	})
	uncertain := mountwrite.MutationResult{OperationID: request.OperationID}
	if msgID > 0 {
		uncertain.CommitRef = strconv.FormatInt(msgID, 10)
	}
	if err != nil {
		return uncertain, mountwrite.ErrCommitOutcomeUnknown
	}
	if request.PersistCommitRef != nil {
		if err := request.PersistCommitRef(uncertain.CommitRef); err != nil {
			return uncertain, mountwrite.ErrCommitOutcomeUnknown
		}
	}
	if err := remote.project(ctx, request.Mutation.DriveID, msgID, op, header); err != nil {
		return uncertain, mountwrite.ErrCommitOutcomeUnknown
	}
	operation, found, err := projection.ProjectionOperationByID(remote.db, request.Mutation.DriveID, request.OperationID)
	if err != nil {
		return uncertain, mountwrite.ErrCommitOutcomeUnknown
	}
	if !found {
		return uncertain, mountwrite.ErrCommitOutcomeUnknown
	}
	if operation.Outcome == projection.OperationRejected {
		return mountwrite.MutationResult{}, mapRejectedProjectionError(operation.Error)
	}
	if operation.Outcome != projection.OperationApplied {
		return uncertain, mountwrite.ErrCommitOutcomeUnknown
	}
	result, err := remote.resultFor(request, msgID)
	if err != nil {
		return uncertain, mountwrite.ErrCommitOutcomeUnknown
	}
	return result, nil
}

var _ mountwrite.ReceiptReconciler = (*TelegramRemote)(nil)

func (remote *TelegramRemote) Reconcile(ctx context.Context, operationID string) (mountwrite.MutationResult, bool, error) {
	if remote == nil {
		return mountwrite.MutationResult{}, false, mountwrite.ErrInvalidRequest
	}
	if result, found, err := remote.reconcileProjected(operationID); err != nil || found {
		return result, found, err
	}
	peer, err := remote.peers.ResolvePeer(ctx, remote.driveID)
	if err != nil {
		return mountwrite.MutationResult{}, false, err
	}
	var offsetID int64
	var candidate tgclient.HistoryMessage
	var candidateOp projection.Op
	candidateFound := false
	for page := 0; page < remote.historyPages; page++ {
		var messages []tgclient.HistoryMessage
		err := remote.floodWaitRetry.Do(ctx, func() error {
			var historyErr error
			messages, historyErr = remote.telegram.GetHistory(ctx, peer, 0, offsetID, remote.historyPageSize)
			return historyErr
		})
		if err != nil {
			return mountwrite.MutationResult{}, false, err
		}
		if len(messages) == 0 {
			break
		}
		for _, message := range messages {
			op, err := projection.Parse(projection.ExtractHeaderLine(message.Text))
			if err != nil || op.OpID != operationID {
				continue
			}
			if !candidateFound || message.MsgID < candidate.MsgID {
				candidate = message
				candidateOp = op
				candidateFound = true
			}
		}
		offsetID = messages[len(messages)-1].MsgID
	}
	if !candidateFound {
		return mountwrite.MutationResult{}, false, nil
	}
	if err := remote.projectHistory(ctx, remote.driveID, candidate.MsgID, candidate.FromID, candidateOp, projection.Format(candidateOp)); err != nil {
		return mountwrite.MutationResult{}, false, err
	}
	result, found, err := remote.reconcileProjected(operationID)
	return result, found, err
}

// ReconcileReceipt fetches the exact Telegram control message accepted by
// Commit. Unlike the legacy bounded history scan, this remains deterministic
// even when a busy drive receives many newer messages before restart.
func (remote *TelegramRemote) ReconcileReceipt(
	ctx context.Context,
	request mountwrite.CommitRequest,
	commitRef string,
) (mountwrite.MutationResult, bool, error) {
	if remote == nil || ctx == nil || request.OperationID == "" || request.Mutation.DriveID != remote.driveID {
		return mountwrite.MutationResult{}, false, mountwrite.ErrInvalidRequest
	}
	msgID, err := strconv.ParseInt(commitRef, 10, 64)
	if err != nil || msgID <= 0 || msgID == math.MaxInt64 {
		return mountwrite.MutationResult{}, false, mountwrite.ErrInvalidRequest
	}
	operation, projected, err := projection.ProjectionOperationByID(remote.db, remote.driveID, request.OperationID)
	if err != nil {
		return mountwrite.MutationResult{}, false, err
	}
	if projected {
		if operation.MsgID != msgID {
			return mountwrite.MutationResult{}, false, mountwrite.ErrConflict
		}
		op, found, err := remote.replayOp(remote.driveID, msgID)
		if err != nil {
			return mountwrite.MutationResult{}, false, err
		}
		if !found || !projectionOperationMatchesCommit(request, op) {
			return mountwrite.MutationResult{}, false, mountwrite.ErrConflict
		}
	}
	if result, found, err := remote.reconcileProjected(request.OperationID); err != nil || found {
		if found && result.CommitRef == "" {
			result.CommitRef = commitRef
		}
		return result, found, err
	}
	peer, err := remote.peers.ResolvePeer(ctx, remote.driveID)
	if err != nil {
		return mountwrite.MutationResult{}, false, err
	}
	var messages []tgclient.HistoryMessage
	err = remote.floodWaitRetry.Do(ctx, func() error {
		var historyErr error
		messages, historyErr = remote.telegram.GetHistory(ctx, peer, msgID-1, msgID+1, 1)
		return historyErr
	})
	if err != nil {
		return mountwrite.MutationResult{}, false, err
	}
	if len(messages) != 1 || messages[0].MsgID != msgID {
		return mountwrite.MutationResult{}, false, nil
	}
	message := messages[0]
	op, err := projection.Parse(projection.ExtractHeaderLine(message.Text))
	if err != nil || !projectionOperationMatchesCommit(request, op) {
		return mountwrite.MutationResult{}, false, mountwrite.ErrConflict
	}
	if err := remote.projectHistory(ctx, remote.driveID, message.MsgID, message.FromID, op, projection.Format(op)); err != nil {
		return mountwrite.MutationResult{}, false, err
	}
	result, found, err := remote.reconcileProjected(request.OperationID)
	if found && result.CommitRef == "" {
		result.CommitRef = commitRef
	}
	return result, found, err
}

func projectionOperationMatchesCommit(request mountwrite.CommitRequest, actual projection.Op) bool {
	if request.CommitTime.IsZero() {
		return false
	}
	expected, err := buildProjectionOperation(request, request.CommitTime.UTC())
	if err != nil {
		return false
	}
	return projection.Format(actual) == projection.Format(expected)
}

func isZeroFloodWaitRetryPolicy(policy tgclient.FloodWaitRetryPolicy) bool {
	return policy.MaxRetries == 0 && policy.MaxWait == 0 && policy.MaxTotalWait == 0 && policy.Sleep == nil
}

func validateFloodWaitRetryPolicy(policy tgclient.FloodWaitRetryPolicy) error {
	if policy.MaxRetries < 0 || policy.MaxWait < 0 || policy.MaxTotalWait < 0 {
		return tgclient.ErrInvalidFloodWaitRetryPolicy
	}
	if policy.MaxRetries > 0 && (policy.MaxWait == 0 || policy.MaxTotalWait == 0) {
		return tgclient.ErrInvalidFloodWaitRetryPolicy
	}
	return nil
}

func (remote *TelegramRemote) reconcileProjected(operationID string) (mountwrite.MutationResult, bool, error) {
	operation, found, err := projection.ProjectionOperationByID(remote.db, remote.driveID, operationID)
	if err != nil || !found {
		return mountwrite.MutationResult{}, found, err
	}
	if operation.Outcome == projection.OperationRejected {
		return mountwrite.MutationResult{}, true, mapRejectedProjectionError(operation.Error)
	}
	result, err := remote.resultForProjected(operation)
	return result, true, err
}

func (remote *TelegramRemote) project(ctx context.Context, driveID, msgID int64, op projection.Op, header string) error {
	actorID, err := remote.actorID(ctx)
	if err != nil {
		return err
	}
	return remote.projectHistory(ctx, driveID, msgID, actorID, op, header)
}

func (remote *TelegramRemote) projectHistory(ctx context.Context, driveID, msgID, actorID int64, op projection.Op, header string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if actorID <= 0 {
		resolved, err := remote.actorID(ctx)
		if err != nil {
			return err
		}
		actorID = resolved
	}
	tx, err := remote.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := projection.ProjectFromOpTx(tx, driveID, msgID, op, actorID, header); err != nil {
		return err
	}
	return tx.Commit()
}

func (remote *TelegramRemote) resultFor(request mountwrite.CommitRequest, msgID int64) (mountwrite.MutationResult, error) {
	result := mountwrite.MutationResult{
		OperationID: request.OperationID,
		CommitRef:   strconv.FormatInt(msgID, 10),
		ObjectID:    request.Mutation.ObjectID,
		Created: request.Mutation.Kind == mountwrite.MutationPut && request.Mutation.ObjectID == "" ||
			request.Mutation.Kind == mountwrite.MutationMkdir ||
			request.Mutation.Kind == mountwrite.MutationMove && request.Mutation.OverwriteTargetID == "",
	}
	if request.Mutation.Kind == mountwrite.MutationPut && result.ObjectID == "" {
		result.ObjectID = projection.FileIDPrefix + strconv.FormatInt(msgID, 10)
	}
	if request.Mutation.Kind == mountwrite.MutationMkdir {
		result.ObjectID = deterministicFolderID(request.OperationID)
	}
	if request.Body != nil {
		result.Size = request.Body.PlaintextSize
		result.SHA256 = request.Body.SHA256
	}
	if result.ObjectID != "" {
		if dirent, found, err := projection.DirentByID(remote.db, request.Mutation.DriveID, result.ObjectID); err == nil && found {
			result.Revision = uint64(dirent.Revision)
		} else if err != nil {
			return mountwrite.MutationResult{}, err
		}
	}
	return result, nil
}

func (remote *TelegramRemote) resultForProjected(operation projection.ProjectionOperation) (mountwrite.MutationResult, error) {
	result := mountwrite.MutationResult{
		OperationID: operation.OpID,
		CommitRef:   strconv.FormatInt(operation.MsgID, 10),
		Created:     operation.OpType == projection.OpFileCommit || operation.OpType == projection.OpFolderCommit,
	}
	objectID, err := remote.objectIDFromProjectionOperation(operation)
	if err != nil {
		return mountwrite.MutationResult{}, err
	}
	result.ObjectID = objectID
	if result.ObjectID != "" {
		if dirent, found, err := projection.DirentByID(remote.db, operation.ChannelID, result.ObjectID); err == nil && found {
			result.Revision = uint64(dirent.Revision)
		} else if err != nil {
			return mountwrite.MutationResult{}, err
		}
	}
	if operation.OpType == projection.OpFileCommit || operation.OpType == projection.OpFileReplace {
		if msgID := fileMessageID(result.ObjectID); msgID > 0 {
			if file, found, err := projection.FileByID(remote.db, operation.ChannelID, msgID); err == nil && found {
				result.Size = file.PlaintextSize
				if result.Size == 0 {
					result.Size = file.Size
				}
				if digest, err := hex.DecodeString(file.ContentHash); err == nil && len(digest) == len(result.SHA256) {
					copy(result.SHA256[:], digest)
				}
			} else if err != nil {
				return mountwrite.MutationResult{}, err
			}
		}
	}
	return result, nil
}

func mapRejectedProjectionError(message string) error {
	switch {
	case strings.Contains(message, projection.ErrRevisionConflict.Error()),
		strings.Contains(message, projection.ErrObjectNotFound.Error()),
		strings.Contains(message, projection.ErrDestinationMismatch.Error()):
		return mountwrite.ErrPreconditionFailed
	default:
		return mountwrite.ErrConflict
	}
}

func buildProjectionOperation(request mountwrite.CommitRequest, now time.Time) (projection.Op, error) {
	op := projection.Op{
		ProtocolVersion: 1,
		OpID:            request.OperationID,
		Parent:          request.Mutation.DestinationParentID,
		Name:            request.Mutation.DestinationName,
	}
	switch request.Mutation.Kind {
	case mountwrite.MutationPut:
		if request.Body == nil {
			return projection.Op{}, mountwrite.ErrInvalidRequest
		}
		op.Type = projection.OpFileCommit
		if request.Mutation.ObjectID != "" {
			op.Type = projection.OpFileReplace
			op.Obj = request.Mutation.ObjectID
			op.ExpectedRevision = int64(request.Mutation.ExpectedRevision)
			op.RetainedUntil = now.Add(defaultRevisionRetention).Unix()
		}
		applyBody(&op, *request.Body, now)
	case mountwrite.MutationMkdir:
		op.Type = projection.OpFolderCommit
		op.Obj = deterministicFolderID(request.OperationID)
	case mountwrite.MutationMove:
		op.Type = projection.OpRelocate
		op.Obj = request.Mutation.ObjectID
		op.ExpectedRevision = int64(request.Mutation.ExpectedRevision)
		op.Overwrite = request.Mutation.OverwriteTargetID != ""
		op.DestinationObj = request.Mutation.OverwriteTargetID
		op.ExpectedDestinationRevision = int64(request.Mutation.ExpectedTargetRevision)
		if op.Overwrite {
			op.DeletedAt = now.Unix()
			op.PurgeAfter = now.Add(defaultTrashRetention).Unix()
		}
	case mountwrite.MutationDelete:
		op.Type = projection.OpTrashTree
		op.Obj = request.Mutation.ObjectID
		op.ExpectedRevision = int64(request.Mutation.ExpectedRevision)
		op.DeletedAt = now.Unix()
		op.PurgeAfter = now.Add(request.Mutation.TrashRetention).Unix()
	default:
		return projection.Op{}, mountwrite.ErrInvalidRequest
	}
	return op, nil
}

func applyBody(op *projection.Op, body mountwrite.RemoteBody, now time.Time) {
	op.FileSize = body.StoredSize
	op.PlaintextSize = body.PlaintextSize
	op.FileUploadTime = now.Unix()
	op.Encrypted = body.Encrypted
	op.EncryptionVersion = int(body.EncryptionVersion)
	if !body.Encrypted {
		op.ContentHash = hex.EncodeToString(body.SHA256[:])
	}
	if body.UploadUUID != "" {
		op.UploadUUID = body.UploadUUID
		op.PartCount = body.PartCount
		return
	}
	if body.ContentRef != "" {
		op.ContentMsgID, _ = strconv.ParseInt(body.ContentRef, 10, 64)
	}
}

func deterministicFolderID(operationID string) string {
	digest := sha256.Sum256([]byte("tdrive.mount.folder.v1\x00" + operationID))
	return projection.FolderIDPrefix + hex.EncodeToString(digest[:16])
}

func (remote *TelegramRemote) objectIDFromProjectionOperation(operation projection.ProjectionOperation) (string, error) {
	switch operation.OpType {
	case projection.OpFileCommit:
		return projection.FileIDPrefix + strconv.FormatInt(operation.MsgID, 10), nil
	case projection.OpFolderCommit:
		return deterministicFolderID(operation.OpID), nil
	case projection.OpFileReplace, projection.OpRelocate, projection.OpTrashTree:
		op, found, err := remote.replayOp(operation.ChannelID, operation.MsgID)
		if err != nil {
			return "", err
		}
		if !found {
			return "", nil
		}
		return op.Obj, nil
	default:
		return "", nil
	}
}

func (remote *TelegramRemote) replayOp(channelID, msgID int64) (projection.Op, bool, error) {
	if remote == nil || remote.db == nil || channelID <= 0 || msgID <= 0 {
		return projection.Op{}, false, mountwrite.ErrInvalidRequest
	}
	var payload string
	err := remote.db.QueryRow(`
		SELECT op_payload_json FROM replay_log
		WHERE channel_id=? AND msg_id=?
	`, channelID, msgID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return projection.Op{}, false, nil
	}
	if err != nil {
		return projection.Op{}, false, err
	}
	var op projection.Op
	if err := json.Unmarshal([]byte(payload), &op); err != nil {
		return projection.Op{}, false, err
	}
	return op, true, nil
}

func fileMessageID(objectID string) int64 {
	if !projection.IsFileID(objectID) {
		return 0
	}
	msgID, _ := strconv.ParseInt(objectID[len(projection.FileIDPrefix):], 10, 64)
	return msgID
}

var _ mountwrite.Remote = (*TelegramRemote)(nil)
