package media

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"
)

// warmReadBytes is the smallest range worth asking for. Nothing is kept; the
// request exists only to make Telegram hand us a file-datacenter connection.
const warmReadBytes = 4 * 1024

// WarmTransport pays the first-read setup cost before a viewer is waiting on it.
//
// The first upload.getFile of a run is consistently seconds slower than the
// ones after it: Telegram answers with FILE_MIGRATE and gotd has to reach the
// file's data center before any bytes move. Measured on a real drive, a 256 KB
// opening read took 4.9s while the 1 MB blocks behind it took 1.0-1.9s, so the
// gap is setup, not transfer. Doing one tiny read at startup moves that cost
// off the path a viewer sees.
//
// Best effort throughout. A failure here only means the first video open pays
// what it used to.
func (s *Service) WarmTransport(ctx context.Context, channelID int64) {
	if s == nil || s.peers == nil || s.ranges == nil || s.resolver == nil {
		return
	}

	started := time.Now()
	fileID, err := s.resolver.anyPlayableFile(ctx, channelID)
	if err != nil {
		slog.Debug("media: no file to warm the transport with", "channel_id", channelID, "error", err)
		return
	}
	file, err := s.resolver.Resolve(ctx, channelID, fileID)
	if err != nil || len(file.Segments) == 0 {
		slog.Debug("media: transport warm could not resolve a file", "channel_id", channelID, "error", err)
		return
	}
	peer, err := s.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		slog.Debug("media: transport warm could not resolve the peer", "channel_id", channelID, "error", err)
		return
	}
	ref, err := s.ranges.ResolveDocument(ctx, peer, file.Segments[0].MsgID)
	if err != nil {
		slog.Debug("media: transport warm could not resolve the document", "channel_id", channelID, "error", err)
		return
	}
	if ref.Size <= 0 {
		return
	}

	size := warmReadBytes
	if ref.Size < int64(size) {
		size = int(ref.Size)
	}
	if _, err := s.ranges.ReadDocumentRange(ctx, ref, 0, make([]byte, size)); err != nil {
		slog.Debug("media: transport warm read failed", "channel_id", channelID, "error", err)
		return
	}
	slog.Info("media: transport warmed", "channel_id", channelID, "elapsed", time.Since(started))
}

// anyPlayableFile picks one live file to warm with. Newest first, because that
// is the one most likely to still exist and to be opened next.
func (r *Resolver) anyPlayableFile(ctx context.Context, channelID int64) (int64, error) {
	if r == nil || r.db == nil {
		return 0, ErrDBNotReady
	}
	var fileID int64
	err := r.db.QueryRowContext(ctx, `
		SELECT msg_id
		FROM files
		WHERE channel_id = ? AND tombstoned = 0 AND size > 0 AND upload_uuid = ''
		ORDER BY msg_id DESC
		LIMIT 1
	`, channelID).Scan(&fileID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrFileNotFound
	}
	if err != nil {
		return 0, err
	}
	return fileID, nil
}
