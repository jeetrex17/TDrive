package tgclient

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

// ResendMedia mimics Telegram copying its stored bytes to another message
// without requiring either the upload or download method on the fake.
func (f *Fake) ResendMedia(ctx context.Context, peer InputPeer, randomID int64, prepare func(context.Context) (ExistingMedia, error)) (SendFileResult, error) {
	if peer.PeerKind() != PeerChannel || peer.ChannelID <= 0 || randomID <= 0 || prepare == nil {
		return SendFileResult{}, fmt.Errorf("tgclient.Fake: invalid existing media send")
	}
	media, err := prepare(ctx)
	if err != nil {
		return SendFileResult{}, err
	}
	ref, caption := media.Document, media.Caption
	if caption == "" {
		return SendFileResult{}, fmt.Errorf("tgclient.Fake: existing media send has no caption")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := sendDedupeKey{ChannelID: peer.ChannelID, RandomID: randomID}
	if result, ok := f.fileSends[key]; ok {
		return result, nil
	}
	if f.failNextSend {
		f.failNextSend = false
		return SendFileResult{}, ErrInjectedSend
	}
	if f.transientFails > 0 {
		f.transientFails--
		return SendFileResult{}, ErrInjectedTransport
	}
	if f.floodWait > 0 {
		f.floodWait--
		return SendFileResult{}, NewFloodWaitError(10 * time.Millisecond)
	}
	id := f.nextMsgID
	f.nextMsgID++
	result := SendFileResult{MsgID: id}
	f.fileSends[key] = result
	f.sentFiles = append(f.sentFiles, SentFile{Peer: peer, Name: ref.Name, Caption: caption, Size: ref.Size, MsgID: id, RandomID: randomID})
	body, found := f.sourceBodies[sourceBodyKey{ref.Peer.PeerKind(), ref.Peer.ChannelID, ref.MsgID}]
	if !found {
		body = f.fileBodies[ref.MsgID]
	}
	f.fileBodies[id] = bytes.Clone(body)
	f.recordMessageEvent(HistoryMessage{
		PeerKind: peer.PeerKind(), ChannelID: peer.ChannelID, MsgID: id, FromID: f.self, Outgoing: true,
		Text: caption, HasMedia: true, DocumentName: ref.Name, MediaSize: ref.Size,
		DocumentID: ref.DocumentID, DocumentAccessHash: ref.AccessHash,
	})
	return result, nil
}

var (
	_ ExistingMediaSender     = (*Fake)(nil)
	_ MessageDocumentResolver = (*Fake)(nil)
)
