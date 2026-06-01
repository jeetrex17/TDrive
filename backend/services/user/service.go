package user

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"TDrive/backend/tgclient"
)

type PeerResolver interface {
	ResolvePeer(ctx context.Context, channelID int64) (tgclient.InputPeer, error)
}

type ActorIDFunc func(ctx context.Context) (int64, error)
type ActiveChannelFunc func() int64

type Service struct {
	DB      *sql.DB
	TG      tgclient.Client
	Peers   PeerResolver
	ActorID ActorIDFunc
	Active  ActiveChannelFunc

	selfCache atomic.Pointer[SelfUser]
}

type SelfUser struct {
	UserID      int64
	DisplayName string
	Username    string
	PhotoBase64 string
}

func (s *Service) Me(ctx context.Context) (SelfUser, error) {
	if cached := s.selfCache.Load(); cached != nil {
		return *cached, nil
	}
	if s.TG == nil {
		return SelfUser{}, fmt.Errorf("tg client not ready")
	}

	profile, err := s.TG.SelfProfile(ctx)
	if err != nil {
		return SelfUser{}, err
	}
	out := SelfUser{
		UserID:      profile.ID,
		DisplayName: fullDisplayName(profile),
		Username:    strings.TrimSpace(profile.Username),
	}
	if len(profile.PhotoBytes) > 0 {
		out.PhotoBase64 = base64.StdEncoding.EncodeToString(profile.PhotoBytes)
	}

	s.selfCache.Store(&out)
	return out, nil
}

func (s *Service) ResolveUsernames(ctx context.Context, userIDs []int64) (map[string]string, error) {
	if s.TG == nil {
		return nil, fmt.Errorf("tg client not ready")
	}

	out := make(map[string]string, len(userIDs))

	self := int64(0)
	if s.ActorID != nil {
		self, _ = s.ActorID(ctx)
	}
	seen := make(map[int64]struct{}, len(userIDs))
	toAsk := make([]int64, 0, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if id == self {
			out[strconv.FormatInt(id, 10)] = "You"
			continue
		}
		toAsk = append(toAsk, id)
	}
	if len(toAsk) == 0 {
		return out, nil
	}

	channelID := int64(0)
	if s.Active != nil {
		channelID = s.Active()
	}
	if channelID == 0 {
		return out, fmt.Errorf("no active channel")
	}
	if s.Peers == nil {
		return out, fmt.Errorf("peer resolver not ready")
	}

	messageRefs, err := uploaderMessageRefs(s.DB, channelID, toAsk)
	if err != nil {
		return out, err
	}
	if len(messageRefs) == 0 {
		return out, nil
	}

	peer, err := s.Peers.ResolvePeer(ctx, channelID)
	if err != nil {
		return out, err
	}
	refs := make([]tgclient.UserMessageRef, 0, len(messageRefs))
	for _, id := range toAsk {
		msgID, ok := messageRefs[id]
		if !ok || msgID <= 0 {
			continue
		}
		refs = append(refs, tgclient.UserMessageRef{UserID: id, MsgID: msgID})
	}
	resolved, err := s.TG.ResolveUsersFromMessages(ctx, peer, refs)
	if err != nil {
		return out, err
	}
	for _, profile := range resolved {
		if profile.ID == 0 {
			continue
		}
		out[strconv.FormatInt(profile.ID, 10)] = pickDisplayName(profile)
	}
	return out, nil
}

func (s *Service) ClearCache() {
	s.selfCache.Store(nil)
}

func uploaderMessageRefs(db *sql.DB, channelID int64, userIDs []int64) (map[int64]int64, error) {
	if db == nil {
		return nil, fmt.Errorf("db not ready")
	}
	out := make(map[int64]int64, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			continue
		}
		var msgID int64
		err := db.QueryRow(`
			SELECT msg_id FROM files
			WHERE channel_id = ? AND uploader_user_id = ? AND tombstoned = 0
			ORDER BY upload_time DESC, msg_id DESC
			LIMIT 1
		`, channelID, id).Scan(&msgID)
		if err == nil {
			out[id] = msgID
			continue
		}
		if err == sql.ErrNoRows {
			continue
		}
		return nil, err
	}
	return out, nil
}

func fullDisplayName(u tgclient.UserProfile) string {
	first := strings.TrimSpace(u.FirstName)
	last := strings.TrimSpace(u.LastName)
	name := strings.TrimSpace(first + " " + last)
	if name != "" {
		return name
	}
	if uname := strings.TrimSpace(u.Username); uname != "" {
		return "@" + uname
	}
	return fmt.Sprintf("User %d", u.ID)
}

func pickDisplayName(u tgclient.UserProfile) string {
	first := strings.TrimSpace(u.FirstName)
	last := strings.TrimSpace(u.LastName)
	username := strings.TrimSpace(u.Username)

	if first != "" {
		if last != "" {
			r := []rune(last)
			if len(r) > 0 {
				return first + " " + strings.ToUpper(string(r[0])) + "."
			}
		}
		return first
	}
	if username != "" {
		return username
	}
	return fmt.Sprintf("User %d", u.ID)
}
