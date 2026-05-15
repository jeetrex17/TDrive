package channel

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

type InitialSyncer interface {
	InitialSyncEmptyChannel(ctx context.Context, channelID int64) error
}

type Service struct {
	DB        *sql.DB
	TG        tgclient.Client
	Sync      InitialSyncer
	GetActive func() int64
	SetActive func(int64)
}

type JoinResult struct {
	Status  string
	Channel *projection.Channel
	Pending *projection.PendingJoin
}

const (
	JoinStatusJoined  = "joined"
	JoinStatusPending = "pending"
)

func (s *Service) ListChannels() ([]projection.Channel, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return projection.ListChannels(s.DB)
}

func (s *Service) CreateSharedDrive(ctx context.Context, title string, requireApproval bool) (projection.Channel, error) {
	if err := s.ready(); err != nil {
		return projection.Channel{}, err
	}
	if title == "" {
		return projection.Channel{}, fmt.Errorf("title required")
	}
	tg, err := s.telegram()
	if err != nil {
		return projection.Channel{}, err
	}
	peer, err := tg.CreateMegagroup(ctx, title, "TDrive shared drive")
	if err != nil {
		return projection.Channel{}, err
	}
	invite, err := tg.ExportInviteLink(ctx, peer, requireApproval)
	if err != nil {
		// Channel exists but no link yet. Save what we have; the
		// "Share" button will retry the export.
		fmt.Printf("warn: invite export failed for new drive %d: %v\n", peer.ChannelID, err)
		invite = ""
	}

	row := projection.Channel{
		ChannelID:            peer.ChannelID,
		AccessHash:           peer.AccessHash,
		Title:                title,
		Kind:                 projection.KindShared,
		InviteLink:           invite,
		PersonalBackfillDone: true,
	}
	if err := projection.InsertChannel(s.DB, row); err != nil {
		return projection.Channel{}, err
	}
	s.setActive(peer.ChannelID)
	return row, nil
}

func (s *Service) JoinSharedDrive(ctx context.Context, inviteLink string) (JoinResult, error) {
	if err := s.ready(); err != nil {
		return JoinResult{}, err
	}
	hash, err := tgclient.ParseInviteHash(inviteLink)
	if err != nil {
		return JoinResult{}, err
	}

	tg, err := s.telegram()
	if err != nil {
		return JoinResult{}, err
	}

	var (
		peer    tgclient.InputPeer
		title   string
		pending bool
	)
	info, err := tg.CheckInvite(ctx, hash)
	if err != nil {
		return JoinResult{}, err
	}
	title = info.Title
	if info.AlreadyJoined {
		peer = tgclient.InputPeer{ChannelID: info.ChannelID, AccessHash: info.AccessHash}
	} else if info.RequestNeeded {
		if err := tg.RequestJoin(ctx, hash); err != nil {
			return JoinResult{}, err
		}
		pending = true
	} else {
		joined, err := tg.JoinByInvite(ctx, hash)
		if err != nil {
			return JoinResult{}, err
		}
		peer = joined
		if resolvedTitle, _ := tg.LookupChannelTitle(ctx, peer); resolvedTitle != "" {
			title = resolvedTitle
		}
		if title == "" {
			title = fmt.Sprintf("Drive %d", peer.ChannelID)
		}
	}

	if pending {
		p := projection.PendingJoin{
			InviteHash:  hash,
			InviteLink:  inviteLink,
			Title:       title,
			RequestedAt: time.Now().Unix(),
			Status:      projection.PendingJoinStatusPending,
		}
		if err := projection.UpsertPendingJoin(s.DB, p); err != nil {
			return JoinResult{}, err
		}
		return JoinResult{Status: JoinStatusPending, Pending: &p}, nil
	}

	ch, err := s.registerJoinedSharedDrive(ctx, peer.ChannelID, peer.AccessHash, title, "")
	if err != nil {
		return JoinResult{}, err
	}
	_ = projection.DeletePendingJoin(s.DB, hash)
	return JoinResult{Status: JoinStatusJoined, Channel: &ch}, nil
}

func (s *Service) ListPendingJoins() ([]projection.PendingJoin, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return projection.ListPendingJoins(s.DB)
}

func (s *Service) CheckPendingJoin(ctx context.Context, inviteHash string) (JoinResult, error) {
	if err := s.ready(); err != nil {
		return JoinResult{}, err
	}
	hash, err := tgclient.ParseInviteHash(inviteHash)
	if err != nil {
		return JoinResult{}, err
	}
	p, err := projection.GetPendingJoin(s.DB, hash)
	if err != nil {
		return JoinResult{}, err
	}

	tg, err := s.telegram()
	if err != nil {
		return s.pendingAfterCheckError(hash, err.Error())
	}

	invite, err := tg.CheckInvite(ctx, hash)
	if err != nil {
		return s.pendingAfterCheckError(hash, err.Error())
	}
	if invite.AlreadyJoined {
		title := invite.Title
		if title == "" {
			title = p.Title
		}
		ch, err := s.registerJoinedSharedDrive(ctx, invite.ChannelID, invite.AccessHash, title, p.InviteLink)
		if err != nil {
			return JoinResult{}, err
		}
		_ = projection.DeletePendingJoin(s.DB, hash)
		return JoinResult{Status: JoinStatusJoined, Channel: &ch}, nil
	}

	_ = projection.UpdatePendingJoinCheck(s.DB, hash, projection.PendingJoinStatusPending, "")
	updated, _ := projection.GetPendingJoin(s.DB, hash)
	return JoinResult{Status: JoinStatusPending, Pending: &updated}, nil
}

func (s *Service) RemovePendingJoin(inviteHash string) error {
	if err := s.ready(); err != nil {
		return err
	}
	hash, err := tgclient.ParseInviteHash(inviteHash)
	if err != nil {
		return err
	}
	return projection.DeletePendingJoin(s.DB, hash)
}

func (s *Service) ExportInviteLink(ctx context.Context, channelID int64, requireApproval bool) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	if channelID == 0 {
		channelID = s.active()
	}
	if channelID == 0 {
		return "", fmt.Errorf("no channel id")
	}

	tg, err := s.telegram()
	if err != nil {
		return "", err
	}
	var link string
	err = s.withInputPeerForChannel(ctx, channelID, func(peer tgclient.InputPeer) error {
		l, err := tg.ExportInviteLink(ctx, peer, requireApproval)
		if err != nil {
			return err
		}
		link = l
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := projection.UpdateInviteLink(s.DB, channelID, link); err != nil {
		fmt.Printf("warn: cache invite link: %v\n", err)
	}
	return link, nil
}

func (s *Service) ListJoinRequests(ctx context.Context, channelID int64) ([]tgclient.JoinRequest, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if channelID == 0 {
		return nil, fmt.Errorf("channel id required")
	}
	tg, err := s.telegram()
	if err != nil {
		return nil, err
	}

	var reqs []tgclient.JoinRequest
	err = s.withInputPeerForChannel(ctx, channelID, func(peer tgclient.InputPeer) error {
		rows, err := tg.ListJoinRequests(ctx, peer)
		if err != nil {
			return err
		}
		reqs = rows
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reqs, nil
}

func (s *Service) HideJoinRequest(ctx context.Context, channelID, userID int64, approved bool) error {
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("channel id required")
	}
	if userID == 0 {
		return fmt.Errorf("user id required")
	}
	tg, err := s.telegram()
	if err != nil {
		return err
	}
	return s.withInputPeerForChannel(ctx, channelID, func(peer tgclient.InputPeer) error {
		reqs, err := tg.ListJoinRequests(ctx, peer)
		if err != nil {
			return err
		}
		var accessHash int64
		for _, r := range reqs {
			if r.UserID == userID {
				accessHash = r.AccessHash
				break
			}
		}
		if accessHash == 0 {
			return fmt.Errorf("join request for user %d not found", userID)
		}
		return tg.HideJoinRequest(ctx, peer, userID, accessHash, approved)
	})
}

func (s *Service) LeaveSharedDrive(ctx context.Context, channelID int64) error {
	if err := s.ready(); err != nil {
		return err
	}
	if channelID == 0 {
		return fmt.Errorf("channel id required")
	}
	c, err := projection.GetChannel(s.DB, channelID)
	if err != nil {
		return err
	}
	if c.Kind != projection.KindShared {
		return fmt.Errorf("can only leave shared drives")
	}

	tg, err := s.telegram()
	if err != nil {
		return err
	}
	peer, err := tg.ResolveDriveChannel(ctx, channelID)
	if err == nil {
		err = tg.LeaveChannel(ctx, peer)
	}
	if err != nil {
		// Even if Telegram refused, still drop local state — the user
		// asked to leave; they can rejoin via invite link if needed.
		fmt.Printf("warn: telegram leave failed: %v\n", err)
	}

	if err := projection.DeleteChannel(s.DB, channelID); err != nil {
		return err
	}

	if s.active() == channelID {
		rows, err := projection.ListChannels(s.DB)
		if err == nil {
			for _, r := range rows {
				if r.Kind == projection.KindPersonal {
					s.setActive(r.ChannelID)
					break
				}
			}
		}
	}
	return nil
}

func (s *Service) pendingAfterCheckError(hash, message string) (JoinResult, error) {
	_ = projection.UpdatePendingJoinCheck(s.DB, hash, projection.PendingJoinStatusError, message)
	updated, _ := projection.GetPendingJoin(s.DB, hash)
	return JoinResult{Status: JoinStatusPending, Pending: &updated}, nil
}

func (s *Service) registerJoinedSharedDrive(ctx context.Context, channelID, accessHash int64, title, inviteLink string) (projection.Channel, error) {
	if channelID == 0 {
		return projection.Channel{}, fmt.Errorf("channel id required")
	}
	if title == "" {
		title = fmt.Sprintf("Drive %d", channelID)
	}
	row := projection.Channel{
		ChannelID:            channelID,
		AccessHash:           accessHash,
		Title:                title,
		Kind:                 projection.KindShared,
		InviteLink:           inviteLink,
		PersonalBackfillDone: true,
	}
	if err := projection.InsertChannel(s.DB, row); err != nil {
		return projection.Channel{}, err
	}
	s.setActive(channelID)

	// Keep this synchronous so callers only return once the joined drive has
	// projected whatever history exists.
	if s.Sync != nil {
		if err := s.Sync.InitialSyncEmptyChannel(ctx, channelID); err != nil {
			fmt.Printf("initial sync failed for joined drive %d: %v\n", channelID, err)
		}
	}

	return row, nil
}

func (s *Service) withInputPeerForChannel(ctx context.Context, channelID int64, fn func(tgclient.InputPeer) error) error {
	peer, fromCache, err := s.inputPeerForChannel(ctx, channelID)
	if err != nil {
		return err
	}
	err = fn(peer)
	if err == nil || !fromCache {
		return err
	}
	if !retryWithFreshPeer(err) {
		return err
	}
	// Access hashes can rotate. If a cached peer fails, resolve fresh once,
	// update the cache, and retry the Telegram call with the exact peer.
	tg, tgErr := s.telegram()
	if tgErr != nil {
		return err
	}
	fresh, resolveErr := tg.ResolveDriveChannel(ctx, channelID)
	if resolveErr != nil {
		return err
	}
	_ = projection.UpdateAccessHash(s.DB, channelID, fresh.AccessHash)
	return fn(fresh)
}

func (s *Service) inputPeerForChannel(ctx context.Context, channelID int64) (tgclient.InputPeer, bool, error) {
	if s.DB != nil {
		if c, err := projection.GetChannel(s.DB, channelID); err == nil && c.AccessHash != 0 {
			return tgclient.InputPeer{ChannelID: channelID, AccessHash: c.AccessHash}, true, nil
		}
	}
	tg, err := s.telegram()
	if err != nil {
		return tgclient.InputPeer{}, false, err
	}
	peer, err := tg.ResolveDriveChannel(ctx, channelID)
	return peer, false, err
}

func retryWithFreshPeer(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "CHANNEL_INVALID") ||
		strings.Contains(msg, "CHANNEL_PRIVATE") ||
		strings.Contains(msg, "PEER_ID_INVALID") ||
		strings.Contains(msg, "ACCESS_HASH")
}

func (s *Service) ready() error {
	if s.DB == nil {
		return fmt.Errorf("db not ready")
	}
	return nil
}

func (s *Service) telegram() (tgclient.Client, error) {
	if s.TG == nil {
		return nil, fmt.Errorf("telegram client not ready")
	}
	return s.TG, nil
}

func (s *Service) active() int64 {
	if s.GetActive == nil {
		return 0
	}
	return s.GetActive()
}

func (s *Service) setActive(channelID int64) {
	if s.SetActive != nil {
		s.SetActive(channelID)
	}
}
