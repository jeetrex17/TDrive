package file

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

type EmitOpFunc func(channelID int64, op projection.Op) (int64, error)
type ActorIDFunc func(ctx context.Context) (int64, error)
type RequireEncryptionKeyFunc func(encrypted bool) error
type MasterKeyForUploadFunc func(channelID int64, wantEncrypted bool) ([]byte, error)
type WriteCiphertextTempFunc func(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error)
type WarnFunc func(format string, args ...any)

type EventSink interface {
	Emit(name string, args ...any)
}

type Service struct {
	DB                   *sql.DB
	TG                   tgclient.Client
	Peers                PeerResolver
	EmitOp               EmitOpFunc
	ActorID              ActorIDFunc
	RequireEncryptionKey RequireEncryptionKeyFunc
	MasterKeyForUpload   MasterKeyForUploadFunc
	WriteCiphertextTemp  WriteCiphertextTempFunc
	Events               EventSink
	Warnf                WarnFunc
	Now                  func() time.Time
}

type Metadata struct {
	Name          string
	Size          int64
	MsgID         int
	ParentID      string
	UploadTime    int64
	Encrypted     bool
	PlaintextSize int64
}

func (s *Service) Upload(ctx context.Context, channelID int64, filePaths []string, parentIDs []string, encrypt bool) ([]Metadata, error) {
	if len(filePaths) != len(parentIDs) {
		return nil, fmt.Errorf("filepaths and parentIDs length mismatch")
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.TG == nil {
		return nil, fmt.Errorf("tg client not ready")
	}
	if s.Peers == nil {
		return nil, fmt.Errorf("peer resolver not ready")
	}
	if channelID == 0 {
		return nil, fmt.Errorf("no active channel")
	}
	if s.ActorID == nil {
		return nil, fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return nil, err
	}

	type uploadedResult struct {
		UploadID  int
		Meta      Metadata
		RawHeader string
		Op        projection.Op
	}

	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex

	uploaded := make([]uploadedResult, 0, len(filePaths))
	failed := 0

	for i := 0; i < len(filePaths); i++ {
		path := filePaths[i]
		pid := parentIDs[i]
		uploadID := i
		wg.Add(1)
		sem <- struct{}{}

		go func(uploadID int, path string, pid string) {
			defer wg.Done()
			defer func() { <-sem }()

			meta, op, header, err := s.uploadSingle(ctx, uploadID, path, pid, channelID, encrypt)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				s.emitEvent("upload_error", uploadID, filepath.Base(path), err.Error())
				return
			}

			mu.Lock()
			uploaded = append(uploaded, uploadedResult{
				UploadID:  uploadID,
				Meta:      meta,
				RawHeader: header,
				Op:        op,
			})
			mu.Unlock()
		}(uploadID, path, pid)
	}

	wg.Wait()

	uploadedFiles := make([]Metadata, 0, len(uploaded))
	for _, item := range uploaded {
		uploadedFiles = append(uploadedFiles, item.Meta)
	}

	emitLocalIndexError := func(reason string) {
		for _, item := range uploaded {
			s.emitEvent("upload_error", item.UploadID, item.Meta.Name, reason)
		}
	}

	for _, item := range uploaded {
		if _, err := projection.ProjectFromOp(
			s.DB,
			channelID,
			int64(item.Meta.MsgID),
			item.Op,
			actorID,
			item.RawHeader,
		); err != nil {
			emitLocalIndexError("local index write failed")
			return uploadedFiles, err
		}
	}

	for _, item := range uploaded {
		s.emitEvent("upload_complete", item.UploadID, item.Meta.Name)
	}

	if failed > 0 {
		return uploadedFiles, fmt.Errorf("%d uploads failed", failed)
	}
	return uploadedFiles, nil
}

func (s *Service) uploadSingle(ctx context.Context, uploadID int, filePath string, parentID string, channelID int64, wantEncrypted bool) (Metadata, projection.Op, string, error) {
	if channelID == 0 {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("drive channel id not found")
	}

	plainFile, err := os.Open(filePath)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	defer plainFile.Close()

	info, err := plainFile.Stat()
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	filename := filepath.Base(filePath)
	plaintextSize := info.Size()

	masterKey, err := s.masterKeyForUpload(channelID, wantEncrypted)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	encrypted := wantEncrypted

	var uploadSource *os.File = plainFile
	uploadSize := plaintextSize
	if encrypted {
		tempCipher, err := s.writeCiphertextTemp(plainFile, plaintextSize, masterKey)
		if err != nil {
			return Metadata{}, projection.Op{}, "", fmt.Errorf("encrypt: %w", err)
		}
		defer func() {
			_ = tempCipher.Close()
			_ = os.Remove(tempCipher.Name())
		}()
		ciphInfo, err := tempCipher.Stat()
		if err != nil {
			return Metadata{}, projection.Op{}, "", err
		}
		uploadSource = tempCipher
		uploadSize = ciphInfo.Size()
	}

	uploadTime := s.now().Unix()
	parent := normalizeParent(parentID)
	op := projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         parent,
		Name:           filename,
		FileSize:       uploadSize,
		FileUploadTime: uploadTime,
	}
	if encrypted {
		op.Encrypted = true
		op.PlaintextSize = plaintextSize
		op.EncryptionVersion = 1
	}
	header := projection.Format(op)
	caption := header + "\nTDrive: " + filename

	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}

	s.warnf("Starting upload: %s\n", filename)
	s.emitEvent("upload_start", uploadID, filename, uploadSize, parentID)

	var (
		lastProgress = time.Now()
		progressMu   sync.Mutex
	)
	result, err := s.TG.SendFile(ctx, peer, uploadSource, filename, caption, uploadSize, func(sent, total int64) {
		progressMu.Lock()
		defer progressMu.Unlock()
		if time.Since(lastProgress) <= 100*time.Millisecond {
			return
		}
		percent := 0.0
		if total > 0 {
			percent = (float64(sent) / float64(total)) * 100
			if percent > 100 {
				percent = 100
			}
		}
		s.emitEvent("upload_progress", uploadID, percent)
		lastProgress = time.Now()
	})
	if err != nil {
		return Metadata{}, projection.Op{}, "", err
	}
	if result.MsgID == 0 {
		return Metadata{}, projection.Op{}, "", fmt.Errorf("upload success, but could not find msgID")
	}

	s.emitEvent("upload_progress", uploadID, 100.0)
	return Metadata{
		Name:          filename,
		Size:          uploadSize,
		MsgID:         int(result.MsgID),
		ParentID:      parent,
		UploadTime:    uploadTime,
		Encrypted:     encrypted,
		PlaintextSize: plaintextSize,
	}, op, header, nil
}

func (s *Service) Meta(channelID int64, msgID int, name string, size int64, parentID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	if msgID <= 0 {
		return fmt.Errorf("Invalid msgID")
	}

	if projection.FileExists(s.DB, channelID, int64(msgID)) {
		return nil
	}

	parent, err := s.validParent(channelID, parentID, "parent")
	if err != nil {
		return err
	}
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		cleanName = "Untitled"
	}

	op := projection.Op{
		Type:           projection.OpMeta,
		Obj:            fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Parent:         parent,
		Name:           cleanName,
		FileSize:       size,
		FileUploadTime: s.now().Unix(),
	}
	_, err = s.emit(channelID, op)
	return err
}

func (s *Service) Rename(ctx context.Context, channelID int64, msgID int, newName string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("Invalid name")
	}
	if !projection.FileExists(s.DB, channelID, int64(msgID)) {
		return fmt.Errorf("File not found")
	}
	if err := s.requireOwnerForShared(ctx, channelID, msgID, "rename"); err != nil {
		return err
	}

	op := projection.Op{
		Type: projection.OpRename,
		Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Name: newName,
	}
	_, err := s.emit(channelID, op)
	return err
}

func (s *Service) Move(channelID int64, msgID int, newParentID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("No active channel")
	}
	parent, err := s.validParent(channelID, newParentID, "target")
	if err != nil {
		return err
	}
	cur, err := projection.FileParent(s.DB, channelID, int64(msgID))
	if err != nil {
		return fmt.Errorf("File not found")
	}
	if cur == parent {
		return fmt.Errorf("File is already in this folder")
	}
	op := projection.Op{
		Type:   projection.OpMove,
		Obj:    fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
		Parent: parent,
	}
	_, err = s.emit(channelID, op)
	return err
}

func (s *Service) Delete(ctx context.Context, channelID int64, msgID int) error {
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("Drive ID not found")
	}
	if !projection.FileExists(s.DB, channelID, int64(msgID)) {
		return fmt.Errorf("File not found")
	}

	if encrypted, _, _, err := projection.FileEncryptionMeta(s.DB, channelID, int64(msgID)); err == nil {
		if err := s.requireEncryptionKey(encrypted); err != nil {
			return err
		}
	}
	if err := s.requireOwnerForShared(ctx, channelID, msgID, "delete"); err != nil {
		return err
	}

	// Tomb first: visibility convergence is the contract; body delete is
	// best-effort. If body cleanup fails, the visible state is still correct.
	tombOp := projection.Op{
		Type: projection.OpTomb,
		Obj:  fmt.Sprintf("%s%d", projection.FileIDPrefix, msgID),
	}
	if _, err := s.emit(channelID, tombOp); err != nil {
		return err
	}

	if s.TG == nil || s.Peers == nil {
		return nil
	}
	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		s.warnf("warn: tomb succeeded but peer resolve failed for msg=%d: %v\n", msgID, err)
		return nil
	}
	if err := s.TG.DeleteMessages(ctx, peer, []int64{int64(msgID)}); err != nil {
		s.warnf("warn: tomb succeeded but body delete failed for msg=%d: %v\n", msgID, err)
	}
	return nil
}

func (s *Service) requireOwnerForShared(ctx context.Context, channelID int64, msgID int, action string) error {
	ch, err := projection.GetChannel(s.DB, channelID)
	if err != nil {
		return err
	}
	if ch.Kind != projection.KindShared {
		return nil
	}
	if s.ActorID == nil {
		return fmt.Errorf("actor resolver not ready")
	}
	actorID, err := s.ActorID(ctx)
	if err != nil {
		return err
	}
	uploader, err := projection.FileUploader(s.DB, channelID, int64(msgID))
	if err != nil {
		return err
	}
	if uploader == 0 || uploader != actorID {
		return fmt.Errorf("Only the uploader can %s this file in a shared drive", action)
	}
	return nil
}

func (s *Service) validParent(channelID int64, parentID string, label string) (string, error) {
	parent := normalizeParent(parentID)
	if parent == projection.RootParent {
		return parent, nil
	}
	if !projection.IsFolderID(parent) {
		if label == "target" {
			return "", fmt.Errorf("Invalid target folder id")
		}
		return "", fmt.Errorf("Invalid parent folder id")
	}
	if !projection.FolderExists(s.DB, channelID, parent) {
		return "", fmt.Errorf("Target folder not found")
	}
	return parent, nil
}

func (s *Service) emit(channelID int64, op projection.Op) (int64, error) {
	if s.EmitOp == nil {
		return 0, fmt.Errorf("file emitter not ready")
	}
	return s.EmitOp(channelID, op)
}

func (s *Service) requireEncryptionKey(encrypted bool) error {
	if s.RequireEncryptionKey == nil {
		return nil
	}
	return s.RequireEncryptionKey(encrypted)
}

func (s *Service) masterKeyForUpload(channelID int64, wantEncrypted bool) ([]byte, error) {
	if s.MasterKeyForUpload == nil {
		if wantEncrypted {
			return nil, fmt.Errorf("encryption upload not ready")
		}
		return nil, nil
	}
	return s.MasterKeyForUpload(channelID, wantEncrypted)
}

func (s *Service) writeCiphertextTemp(plain io.Reader, plaintextSize int64, masterKey []byte) (*os.File, error) {
	if s.WriteCiphertextTemp == nil {
		return nil, fmt.Errorf("encryption upload not ready")
	}
	return s.WriteCiphertextTemp(plain, plaintextSize, masterKey)
}

func (s *Service) emitEvent(name string, args ...any) {
	if s.Events != nil {
		s.Events.Emit(name, args...)
	}
}

func (s *Service) ready() error {
	if s.DB == nil {
		return fmt.Errorf("DB not ready")
	}
	return nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) warnf(format string, args ...any) {
	if s.Warnf != nil {
		s.Warnf(format, args...)
	}
}

func normalizeParent(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return projection.RootParent
	}
	return p
}
