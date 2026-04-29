package tgclient

import (
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

	self         int64
	nextMsgID    int64
	history      []HistoryMessage // ordered by msg_id ascending
	sentControls []SentControl
	sentFiles    []SentFile
	deletedBatch [][]int64
	floodWait    int // counter; pre-injects ErrFloodWait this many times before succeeding
	failNextSend bool
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

var (
	ErrInjectedSend = errors.New("tgclient.Fake: injected send failure")
)

func NewFake(selfID int64) *Fake {
	return &Fake{
		self:      selfID,
		nextMsgID: 100,
	}
}

func (f *Fake) SetSelfID(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.self = id
}

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

	// Drain the reader so callers passing real Readers don't hang.
	if r != nil {
		var sent int64
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
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
	f.history = append(f.history, HistoryMessage{
		MsgID:     id,
		Date:      0,
		FromID:    f.self,
		Text:      caption,
		HasMedia:  true,
		MediaSize: totalSize,
	})
	return SendFileResult{MsgID: id}, nil
}

func (f *Fake) GetHistory(ctx context.Context, peer InputPeer, minID, offsetID int64, limit int) ([]HistoryMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

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
