package tgclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

// Fake is an in-memory implementation of Client used by tests. It records
// every send (so tests can assert on TDX1 captions and silent flags), and
// it serves history out of an injectable buffer.
//
// Methods are goroutine-safe.
type Fake struct {
	mu sync.Mutex

	self          int64
	nextMsgID     int64
	nextChannelID int64
	history       []HistoryMessage // ordered by msg_id ascending
	fileBodies    map[int64][]byte
	sentControls  []SentControl
	sentFiles     []SentFile
	deletedBatch  [][]int64
	floodWait     int // counter; pre-injects ErrFloodWait this many times before succeeding
	readFloodWait int // counter; pre-injects ErrFloodWait on GetHistory this many times
	failNextSend  bool

	channels       map[int64]fakeChannel
	invites        map[string]InviteInfo
	joinRequests   map[int64][]JoinRequest
	requestedJoins []string
	hiddenRequests []HiddenJoinRequest
	leftChannels   []InputPeer
	users          map[int64]UserProfile
	selfCalls      int
	resolveCalls   int
}

type SentControl struct {
	Peer   InputPeer
	Text   string
	Silent bool
	MsgID  int64
}

type SentFile struct {
	Peer    InputPeer
	Name    string
	Caption string
	Size    int64
	MsgID   int64
}

type HiddenJoinRequest struct {
	Peer     InputPeer
	UserID   int64
	Approved bool
}

type fakeChannel struct {
	Peer  InputPeer
	Title string
	About string
}

var (
	ErrInjectedSend = errors.New("tgclient.Fake: injected send failure")
)

func NewFake(selfID int64) *Fake {
	return &Fake{
		self:          selfID,
		nextMsgID:     100,
		nextChannelID: 10000,
		fileBodies:    make(map[int64][]byte),
		channels:      make(map[int64]fakeChannel),
		invites:       make(map[string]InviteInfo),
		joinRequests:  make(map[int64][]JoinRequest),
		users:         make(map[int64]UserProfile),
	}
}

func (f *Fake) SetSelfID(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.self = id
}

// Close satisfies Client. The fake holds no connection.
func (f *Fake) Close() {}

// SeedHistory pre-loads messages as if they already existed in the channel.
// Useful for sync tests. Auto-sorts ascending by msg_id.
func (f *Fake) SeedHistory(msgs ...HistoryMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.history = append(f.history, msgs...)
	sort.Slice(f.history, func(i, j int) bool { return f.history[i].MsgID < f.history[j].MsgID })
	for _, m := range f.history {
		if m.MsgID >= f.nextMsgID {
			f.nextMsgID = m.MsgID + 1
		}
	}
}

// InjectFloodWaits causes the next n sends (control or file) to fail with
// ErrFloodWait before succeeding. Drives backoff tests.
func (f *Fake) InjectFloodWaits(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.floodWait = n
}

// InjectReadFloodWaits causes the next n GetHistory calls to fail with
// ErrFloodWait before succeeding. Drives read-side backoff tests.
func (f *Fake) InjectReadFloodWaits(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readFloodWait = n
}

// FailNextSend causes the next send to fail with ErrInjectedSend, then
// resumes normal behavior.
func (f *Fake) FailNextSend() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNextSend = true
}

func (f *Fake) SentControls() []SentControl {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]SentControl, len(f.sentControls))
	copy(out, f.sentControls)
	return out
}

func (f *Fake) SentFiles() []SentFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]SentFile, len(f.sentFiles))
	copy(out, f.sentFiles)
	return out
}

func (f *Fake) DeletedBatches() [][]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]int64, len(f.deletedBatch))
	for i, b := range f.deletedBatch {
		c := make([]int64, len(b))
		copy(c, b)
		out[i] = c
	}
	return out
}

func (f *Fake) RequestedJoins() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.requestedJoins))
	copy(out, f.requestedJoins)
	return out
}

func (f *Fake) HiddenJoinRequests() []HiddenJoinRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]HiddenJoinRequest, len(f.hiddenRequests))
	copy(out, f.hiddenRequests)
	return out
}

func (f *Fake) LeftChannels() []InputPeer {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]InputPeer, len(f.leftChannels))
	copy(out, f.leftChannels)
	return out
}

func (f *Fake) SeedChannel(peer InputPeer, title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channels[peer.ChannelID] = fakeChannel{Peer: peer, Title: title}
	if peer.ChannelID >= f.nextChannelID {
		f.nextChannelID = peer.ChannelID + 1
	}
}

func (f *Fake) SeedInvite(hash string, info InviteInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invites[hash] = info
	if info.ChannelID != 0 {
		f.channels[info.ChannelID] = fakeChannel{
			Peer:  InputPeer{ChannelID: info.ChannelID, AccessHash: info.AccessHash},
			Title: info.Title,
		}
		if info.ChannelID >= f.nextChannelID {
			f.nextChannelID = info.ChannelID + 1
		}
	}
}

func (f *Fake) SeedJoinRequests(channelID int64, reqs ...JoinRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]JoinRequest, len(reqs))
	copy(cp, reqs)
	f.joinRequests[channelID] = cp
}

func (f *Fake) SeedUser(user UserProfile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[user.ID] = user
}

func (f *Fake) SelfProfileCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.selfCalls
}

func (f *Fake) ResolveUsersFromMessagesCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resolveCalls
}

// EditLastControlText simulates a member editing a TDX1 caption from the
// regular Telegram client. The history record's text changes; msg_id stays
// the same. Returns the msg_id mutated.
func (f *Fake) EditLastControlText(newText string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sentControls) == 0 {
		return 0, fmt.Errorf("tgclient.Fake: no controls to edit")
	}
	last := &f.sentControls[len(f.sentControls)-1]
	last.Text = newText
	for i := range f.history {
		if f.history[i].MsgID == last.MsgID {
			f.history[i].Text = newText
			return last.MsgID, nil
		}
	}
	return 0, fmt.Errorf("tgclient.Fake: msg %d not in history", last.MsgID)
}

func (f *Fake) SelfID(ctx context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.self, nil
}

func (f *Fake) SelfProfile(ctx context.Context) (UserProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.selfCalls++
	if user, ok := f.users[f.self]; ok {
		return user, nil
	}
	return UserProfile{ID: f.self, FirstName: "Self"}, nil
}

func (f *Fake) ResolveUsersFromMessages(ctx context.Context, peer InputPeer, refs []UserMessageRef) ([]UserProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveCalls++
	out := make([]UserProfile, 0, len(refs))
	for _, ref := range refs {
		if user, ok := f.users[ref.UserID]; ok {
			out = append(out, user)
		}
	}
	return out, nil
}

func (f *Fake) SendControl(ctx context.Context, peer InputPeer, text string, silent bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNextSend {
		f.failNextSend = false
		return 0, ErrInjectedSend
	}
	if f.floodWait > 0 {
		f.floodWait--
		return 0, NewFloodWaitError(10 * time.Millisecond)
	}
	id := f.nextMsgID
	f.nextMsgID++
	f.sentControls = append(f.sentControls, SentControl{Peer: peer, Text: text, Silent: silent, MsgID: id})
	f.history = append(f.history, HistoryMessage{
		MsgID:     id,
		Date:      0,
		FromID:    f.self,
		Text:      text,
		HasMedia:  false,
		MediaSize: 0,
	})
	return id, nil
}

func (f *Fake) SendFile(ctx context.Context, peer InputPeer, r io.Reader, name, caption string, totalSize int64, onProgress func(sent, total int64)) (SendFileResult, error) {
	f.mu.Lock()
	if f.failNextSend {
		f.failNextSend = false
		f.mu.Unlock()
		return SendFileResult{}, ErrInjectedSend
	}
	if f.floodWait > 0 {
		f.floodWait--
		f.mu.Unlock()
		return SendFileResult{}, NewFloodWaitError(10 * time.Millisecond)
	}
	f.mu.Unlock()

	var body bytes.Buffer
	// Drain the reader so callers passing real Readers don't hang.
	if r != nil {
		var sent int64
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				_, _ = body.Write(buf[:n])
				sent += int64(n)
				if onProgress != nil {
					onProgress(sent, totalSize)
				}
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return SendFileResult{}, err
			}
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextMsgID
	f.nextMsgID++
	f.sentFiles = append(f.sentFiles, SentFile{Peer: peer, Name: name, Caption: caption, Size: totalSize, MsgID: id})
	f.fileBodies[id] = append([]byte(nil), body.Bytes()...)
	f.history = append(f.history, HistoryMessage{
		MsgID:        id,
		Date:         0,
		FromID:       f.self,
		Text:         caption,
		HasMedia:     true,
		MediaSize:    totalSize,
		DocumentName: name,
	})
	return SendFileResult{MsgID: id}, nil
}

func (f *Fake) GetHistory(ctx context.Context, peer InputPeer, minID, offsetID int64, limit int) ([]HistoryMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.readFloodWait > 0 {
		f.readFloodWait--
		return nil, NewFloodWaitError(time.Millisecond)
	}

	if limit <= 0 {
		limit = 100
	}
	out := make([]HistoryMessage, 0, limit)

	// Telegram history is newest-first. minID restricts the lower bound;
	// offsetID pages older than a prior page.
	for i := len(f.history) - 1; i >= 0; i-- {
		m := f.history[i]
		if minID > 0 && m.MsgID <= minID {
			continue
		}
		if offsetID > 0 && m.MsgID >= offsetID {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *Fake) GetFileDocument(ctx context.Context, peer InputPeer, msgID int64) (FileDocument, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.history {
		if m.MsgID != msgID {
			continue
		}
		if !m.HasMedia {
			return FileDocument{}, ErrNotFile
		}
		name := m.DocumentName
		if name == "" {
			name = "tdrive_download"
		}
		thumbs := make([]FileThumb, len(m.Thumbs))
		copy(thumbs, m.Thumbs)
		return FileDocument{MsgID: msgID, Name: name, Size: m.MediaSize, Thumbs: thumbs}, nil
	}
	return FileDocument{}, ErrMessageNotFound
}

func (f *Fake) DownloadFile(ctx context.Context, peer InputPeer, msgID int64, w io.Writer, onProgress func(done, total int64)) error {
	f.mu.Lock()
	var (
		msg   HistoryMessage
		found bool
	)
	for _, m := range f.history {
		if m.MsgID == msgID {
			msg = m
			found = true
			break
		}
	}
	if !found {
		f.mu.Unlock()
		return ErrMessageNotFound
	}
	if !msg.HasMedia {
		f.mu.Unlock()
		return ErrNotFile
	}
	body := append([]byte(nil), f.fileBodies[msgID]...)
	f.mu.Unlock()

	if len(body) == 0 && msg.MediaSize > 0 {
		return ErrEmptyDocument
	}
	n, err := w.Write(body)
	if onProgress != nil {
		onProgress(int64(n), msg.MediaSize)
	}
	return err
}

func (f *Fake) DownloadFileThumbnail(ctx context.Context, peer InputPeer, msgID int64, thumbType string, w io.Writer) error {
	f.mu.Lock()
	var (
		msg   HistoryMessage
		found bool
	)
	for _, m := range f.history {
		if m.MsgID == msgID {
			msg = m
			found = true
			break
		}
	}
	f.mu.Unlock()
	if !found {
		return ErrMessageNotFound
	}
	if !msg.HasMedia {
		return ErrNotFile
	}
	for _, thumb := range msg.Thumbs {
		if thumb.Type == thumbType {
			if len(thumb.Bytes) == 0 {
				return ErrEmptyDocument
			}
			_, err := w.Write(thumb.Bytes)
			return err
		}
	}
	return ErrEmptyDocument
}

func (f *Fake) DeleteMessages(ctx context.Context, peer InputPeer, msgIDs []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(msgIDs) == 0 {
		return nil
	}
	cp := make([]int64, len(msgIDs))
	copy(cp, msgIDs)
	f.deletedBatch = append(f.deletedBatch, cp)

	doomed := make(map[int64]struct{}, len(msgIDs))
	for _, id := range msgIDs {
		doomed[id] = struct{}{}
		delete(f.fileBodies, id)
	}
	kept := f.history[:0]
	for _, m := range f.history {
		if _, ok := doomed[m.MsgID]; ok {
			continue
		}
		kept = append(kept, m)
	}
	f.history = kept
	return nil
}

func (f *Fake) CreateMegagroup(ctx context.Context, title, about string) (InputPeer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextChannelID
	f.nextChannelID++
	peer := InputPeer{ChannelID: id, AccessHash: id + 1000}
	f.channels[id] = fakeChannel{Peer: peer, Title: title, About: about}
	return peer, nil
}

func (f *Fake) ExportInviteLink(ctx context.Context, peer InputPeer, requestNeeded bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hash := fmt.Sprintf("fake-%d", peer.ChannelID)
	if requestNeeded {
		hash = fmt.Sprintf("approval-%d", peer.ChannelID)
	}
	ch := f.channels[peer.ChannelID]
	f.invites[hash] = InviteInfo{
		RequestNeeded: requestNeeded,
		Title:         ch.Title,
		ChannelID:     peer.ChannelID,
		AccessHash:    peer.AccessHash,
	}
	return "https://t.me/+" + hash, nil
}

func (f *Fake) CheckInvite(ctx context.Context, hash string) (InviteInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.invites[hash]
	if !ok {
		return InviteInfo{}, fmt.Errorf("tgclient.Fake: invite %q not found", hash)
	}
	return info, nil
}

func (f *Fake) RequestJoin(ctx context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requestedJoins = append(f.requestedJoins, hash)
	return nil
}

func (f *Fake) JoinByInvite(ctx context.Context, hash string) (InputPeer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.invites[hash]
	if !ok {
		return InputPeer{}, fmt.Errorf("tgclient.Fake: invite %q not found", hash)
	}
	if info.ChannelID == 0 {
		return InputPeer{}, fmt.Errorf("tgclient.Fake: invite %q has no channel", hash)
	}
	peer := InputPeer{ChannelID: info.ChannelID, AccessHash: info.AccessHash}
	f.channels[peer.ChannelID] = fakeChannel{Peer: peer, Title: info.Title}
	return peer, nil
}

func (f *Fake) LookupChannelTitle(ctx context.Context, peer InputPeer) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.channels[peer.ChannelID]
	if !ok {
		return "", nil
	}
	return ch.Title, nil
}

func (f *Fake) ListJoinRequests(ctx context.Context, peer InputPeer) ([]JoinRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reqs := f.joinRequests[peer.ChannelID]
	out := make([]JoinRequest, len(reqs))
	copy(out, reqs)
	return out, nil
}

func (f *Fake) HideJoinRequest(ctx context.Context, peer InputPeer, userID, accessHash int64, approved bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hiddenRequests = append(f.hiddenRequests, HiddenJoinRequest{Peer: peer, UserID: userID, Approved: approved})
	reqs := f.joinRequests[peer.ChannelID]
	kept := reqs[:0]
	for _, r := range reqs {
		if r.UserID == userID {
			continue
		}
		kept = append(kept, r)
	}
	f.joinRequests[peer.ChannelID] = kept
	return nil
}

func (f *Fake) ResolveDriveChannel(ctx context.Context, channelID int64) (InputPeer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.channels[channelID]
	if !ok {
		return InputPeer{}, fmt.Errorf("tgclient.Fake: channel %d not found", channelID)
	}
	return ch.Peer, nil
}

func (f *Fake) LeaveChannel(ctx context.Context, peer InputPeer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leftChannels = append(f.leftChannels, peer)
	delete(f.channels, peer.ChannelID)
	return nil
}
