package media

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"TDrive/backend/tgclient"
)

type canceledCheckClient struct {
	tgclient.Client
	entered chan struct{}
	calls   atomic.Int32
}

type notifyingContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *notifyingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func (c *canceledCheckClient) GetMediaSourcePeer(ctx context.Context, peer tgclient.InputPeer) (tgclient.SourcePeer, error) {
	if c.calls.Add(1) == 1 {
		close(c.entered)
		<-ctx.Done()
		return tgclient.SourcePeer{}, ctx.Err()
	}
	return c.Client.GetMediaSourcePeer(ctx, peer)
}

func TestExternalUncachedReadPreservesOpenedProtectionPolicy(t *testing.T) {
	for _, protected := range []bool{false, true} {
		t.Run(fmt.Sprintf("opened protected=%v", protected), func(t *testing.T) {
			fake := tgclient.NewFake(1234)
			peer := tgclient.InputPeer{Kind: tgclient.PeerGroup, ChannelID: 42}
			fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 42, Title: "Friends", Protected: protected})
			fake.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerGroup, ChannelID: 42, MsgID: 7,
				HasMedia: true, DocumentID: 701, DocumentAccessHash: 702, MediaSize: 8, DocumentName: "clip.mp4"})
			fake.SeedSourceDocumentBody(tgclient.PeerGroup, 42, 7, []byte("12345678"))
			ref, err := fake.ResolveDocument(t.Context(), peer, 7)
			if err != nil {
				t.Fatal(err)
			}
			reader := &externalRangeClient{base: fake, client: fake, peer: peer, original: ref, protected: protected}
			reader.lastRemoteCheck.Store(time.Now().UnixNano())
			buf := make([]byte, 8)
			if n, err := reader.ReadDocumentRange(t.Context(), ref, 0, buf); err != nil || n != 8 {
				t.Fatalf("initial range=%d, %v", n, err)
			}
			fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 42, Title: "Friends", Protected: true})
			reader.lastRemoteCheck.Store(0)
			n, err := reader.ReadDocumentRange(t.Context(), ref, 0, buf)
			if protected {
				if err != nil || n != 8 || string(buf) != "12345678" {
					t.Fatalf("protected range=%d, %v", n, err)
				}
			} else if n != 0 || !errors.Is(err, ErrExternalRestricted) {
				t.Fatalf("newly protected range=%d, %v", n, err)
			}
		})
	}
}

func TestExternalRangeChecksLocalGenerationBeforeCachedAccessWindow(t *testing.T) {
	fake := tgclient.NewFake(1234)
	peer := tgclient.InputPeer{Kind: tgclient.PeerUser, ChannelID: 42, AccessHash: 100}
	ref := tgclient.DocumentRef{Peer: peer, MsgID: 7, Size: 8, DocumentID: 701}
	stale := errors.New("connection generation changed")
	reader := &externalRangeClient{base: fake, client: fake, peer: peer, original: ref,
		validate: func(context.Context) error { return stale }}
	reader.lastRemoteCheck.Store(time.Now().UnixNano())
	if n, err := reader.ReadDocumentRange(t.Context(), ref, 0, make([]byte, 8)); n != 0 || !errors.Is(err, stale) {
		t.Fatalf("stale range = %d, %v", n, err)
	}
}

func TestExternalRangeRetriesAfterCanceledConcurrentCheck(t *testing.T) {
	fake := tgclient.NewFake(1234)
	peer := tgclient.InputPeer{Kind: tgclient.PeerGroup, ChannelID: 42}
	fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 42, Title: "Friends"})
	fake.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerGroup, ChannelID: 42, MsgID: 7,
		HasMedia: true, DocumentID: 701, DocumentAccessHash: 702, MediaSize: 8, DocumentName: "clip.mp4"})
	fake.SeedSourceDocumentBody(tgclient.PeerGroup, 42, 7, []byte("12345678"))
	ref, err := fake.ResolveDocument(t.Context(), peer, 7)
	if err != nil {
		t.Fatal(err)
	}
	client := &canceledCheckClient{Client: fake, entered: make(chan struct{})}
	reader := &externalRangeClient{base: fake, client: client, peer: peer, original: ref}
	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leaderResult := make(chan error, 1)
	go func() {
		_, err := reader.ReadDocumentRange(leaderCtx, ref, 0, make([]byte, 8))
		leaderResult <- err
	}()
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("leader never entered remote access check")
	}
	followerCtx := &notifyingContext{Context: t.Context(), entered: make(chan struct{})}
	followerResult := make(chan error, 1)
	go func() {
		_, err := reader.ReadDocumentRange(followerCtx, ref, 0, make([]byte, 8))
		followerResult <- err
	}()
	// Done is evaluated only after the follower reaches the wait for the
	// in-flight access check. This barrier makes the overlap deterministic.
	select {
	case <-followerCtx.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("follower never waited for remote access check")
	}
	cancelLeader()
	var leaderErr error
	select {
	case leaderErr = <-leaderResult:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled leader did not finish")
	}
	if err := leaderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader = %v, want cancellation", err)
	}
	var followerErr error
	select {
	case followerErr = <-followerResult:
	case <-time.After(5 * time.Second):
		t.Fatal("live follower did not finish")
	}
	if err := followerErr; err != nil {
		t.Fatalf("live follower inherited canceled leader: %v", err)
	}
	if got := client.calls.Load(); got != 2 {
		t.Fatalf("remote checks = %d, want canceled leader plus live retry", got)
	}
}

func TestExternalPhotoReferenceRefreshPreservesVariantAndIdentity(t *testing.T) {
	for _, change := range []string{"", "photo variant", "document", "peer kind"} {
		t.Run(change, func(t *testing.T) {
			fake := tgclient.NewFake(1234)
			peer := tgclient.InputPeer{Kind: tgclient.PeerGroup, ChannelID: 42}
			fake.SeedMediaSourcePeers(tgclient.SourcePeer{Kind: tgclient.PeerGroup, ID: 42, Title: "Photos"})
			fake.SeedHistory(tgclient.HistoryMessage{PeerKind: tgclient.PeerGroup, ChannelID: 42, MsgID: 7,
				HasMedia: true, DocumentID: 701, DocumentAccessHash: 702, MediaSize: 8, DocumentName: "photo.jpg", MimeType: "image/jpeg"})
			original, err := fake.ResolveDocument(t.Context(), peer, 7)
			if err != nil {
				t.Fatal(err)
			}
			original.PhotoSizeType = "x"
			reader := &externalRangeClient{base: &changingRawReference{RangeClient: fake, change: change}, client: fake, peer: peer, original: original}
			ref, err := reader.ResolveDocument(t.Context(), peer, 7)
			if change == "" {
				if err != nil || ref.PhotoSizeType != "x" {
					t.Fatalf("photo refresh=%#v, %v", ref, err)
				}
			} else if !errors.Is(err, ErrExternalReplaced) {
				t.Fatalf("changed %s accepted: %v", change, err)
			}
		})
	}
}
