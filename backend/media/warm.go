package media

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"TDrive/backend/tgclient"
)

const (
	// warmReadBytes is the smallest range worth asking for. Nothing is kept;
	// the requests exist only to make Telegram hand us file-datacenter
	// connections.
	warmReadBytes = 4 * 1024

	// warmFileLimit is how many of the newest files the warm-up inspects to
	// learn which data centers a drive's files live on. An upload lands on the
	// uploader's data center, so a drive shared by a few people spans a few.
	warmFileLimit = 16
)

// WarmTransport pays the first-read setup cost before a viewer is waiting on it.
//
// The first upload.getFile of a run is consistently seconds slower than the
// ones after it: reaching the file's data center means a key exchange and an
// authorization import per connection before any bytes move. Measured on a
// real drive, a 256 KB opening read took 4.9s while the 1 MB blocks behind it
// took 1.0-1.9s, so the gap is setup, not transfer. One tiny read per pooled
// connection dials a data center's whole pool (gotd opens a connection for
// each request that finds none free), and doing it for every data center the
// newest files live on moves that cost off the path a viewer sees.
//
// Best effort throughout. A failure here only means the first video open pays
// what it used to.
func (s *Service) WarmTransport(ctx context.Context, channelID int64) {
	if s == nil || s.peers == nil || s.ranges == nil || s.resolver == nil {
		return
	}

	started := time.Now()
	fileIDs, err := s.resolver.newestPlayableFiles(ctx, channelID, warmFileLimit)
	if err != nil || len(fileIDs) == 0 {
		slog.Debug("media: no file to warm the transport with", "channel_id", channelID, "error", err)
		return
	}
	segments := make([]Segment, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		file, err := s.resolver.Resolve(ctx, channelID, fileID)
		if err != nil || len(file.Segments) == 0 {
			continue
		}
		segments = append(segments, file.Segments[0])
	}
	if len(segments) == 0 {
		return
	}
	peer, err := s.peers.ResolvePeer(ctx, channelID)
	if err != nil {
		slog.Debug("media: transport warm could not resolve the peer", "channel_id", channelID, "error", err)
		return
	}
	refs, err := s.resolveSegments(ctx, peer, segments)
	if err != nil {
		slog.Debug("media: transport warm could not resolve the documents", "channel_id", channelID, "error", err)
		return
	}

	// One file per data center is enough to dial that center's pool.
	perDC := make(map[int]tgclient.DocumentRef, len(refs))
	for _, ref := range refs {
		if _, seen := perDC[ref.DCID]; !seen && ref.Size > 0 {
			perDC[ref.DCID] = ref
		}
	}
	var wg sync.WaitGroup
	for dc, ref := range perDC {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reads := s.warmPool(ctx, ref)
			slog.Info("media: transport warmed", "channel_id", channelID, "dc", dc, "reads", reads, "elapsed", time.Since(started))
		}()
	}
	wg.Wait()
}

// warmPool issues one tiny read per pooled connection, at distinct aligned
// offsets so a small file still yields as many reads as it has room for, and
// reports how many it made.
func (s *Service) warmPool(ctx context.Context, ref tgclient.DocumentRef) int {
	var wg sync.WaitGroup
	reads := 0
	for i := range tgclient.MediaPoolSize {
		offset := int64(i) * tgclient.RangeReadAlignment
		if offset >= ref.Size {
			break
		}
		size := int(min(int64(warmReadBytes), ref.Size-offset))
		reads++
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ranges.ReadDocumentRange(ctx, ref, offset, make([]byte, size)); err != nil {
				slog.Debug("media: transport warm read failed", "dc", ref.DCID, "offset", offset, "error", err)
			}
		}()
	}
	wg.Wait()
	return reads
}

// newestPlayableFiles lists up to limit live files, newest first, because
// those are the ones most likely to still exist and to be opened next.
func (r *Resolver) newestPlayableFiles(ctx context.Context, channelID int64, limit int) ([]int64, error) {
	if r == nil || r.db == nil {
		return nil, ErrDBNotReady
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT msg_id
		FROM files
		WHERE channel_id = ? AND tombstoned = 0 AND size > 0 AND upload_uuid = ''
		ORDER BY msg_id DESC
		LIMIT ?
	`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fileIDs []int64
	for rows.Next() {
		var fileID int64
		if err := rows.Scan(&fileID); err != nil {
			return nil, err
		}
		fileIDs = append(fileIDs, fileID)
	}
	return fileIDs, rows.Err()
}
