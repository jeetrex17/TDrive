package tgclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
)

type scriptedDialogsQuery struct {
	responses []tg.MessagesDialogsClass
	errAt     int
	err       error
	calls     []dialogs.Request
}

func (q *scriptedDialogsQuery) Query(_ context.Context, request dialogs.Request) (tg.MessagesDialogsClass, error) {
	q.calls = append(q.calls, request)
	call := len(q.calls)
	if q.err != nil && call == q.errAt {
		return nil, q.err
	}
	if call <= len(q.responses) {
		return q.responses[call-1], nil
	}
	return &tg.MessagesDialogsSlice{}, nil
}

type dialogChannel struct {
	id          int64
	title       string
	creator     bool
	broadcast   bool
	megagroup   bool
	left        bool
	topMessage  int
	createdDate int
}

func dialogsPage(rows ...dialogChannel) tg.MessagesDialogsClass {
	result := &tg.MessagesDialogsSlice{Count: len(rows)}
	for _, row := range rows {
		peer := &tg.PeerChannel{ChannelID: row.id}
		result.Dialogs = append(result.Dialogs, &tg.Dialog{
			Peer:       peer,
			TopMessage: row.topMessage,
		})
		result.Chats = append(result.Chats, &tg.Channel{
			ID:         row.id,
			AccessHash: row.id + 1000,
			Title:      row.title,
			Creator:    row.creator,
			Broadcast:  row.broadcast,
			Megagroup:  row.megagroup,
			Left:       row.left,
			Date:       row.createdDate,
		})
		if row.topMessage > 0 {
			result.Messages = append(result.Messages, &tg.Message{
				ID:     row.topMessage,
				PeerID: peer,
				Date:   row.createdDate,
			})
		}
	}
	return result
}

func TestCollectOwnedBroadcastChannelsPaginatesFiltersAndDeduplicates(t *testing.T) {
	main := &scriptedDialogsQuery{responses: []tg.MessagesDialogsClass{
		dialogsPage(
			dialogChannel{id: 1001, title: "TDrive", creator: true, broadcast: true, topMessage: 90, createdDate: 100},
			dialogChannel{id: 2001, title: "Joined", creator: false, broadcast: true, topMessage: 8, createdDate: 10},
			dialogChannel{id: 2002, title: "Group", creator: true, megagroup: true, topMessage: 8, createdDate: 10},
		),
		dialogsPage(
			dialogChannel{id: 1003, title: "Archive", creator: true, broadcast: true, topMessage: 7, createdDate: 25},
			dialogChannel{id: 2003, title: "Left", creator: true, broadcast: true, left: true, topMessage: 8, createdDate: 10},
		),
		&tg.MessagesDialogsSlice{},
	}}
	archive := &scriptedDialogsQuery{responses: []tg.MessagesDialogsClass{
		dialogsPage(
			dialogChannel{id: 1001, title: "TDrive", creator: true, broadcast: true, topMessage: 90, createdDate: 100},
			dialogChannel{id: 1002, title: "TDrive", creator: true, broadcast: true, createdDate: 50},
		),
		&tg.MessagesDialogsSlice{},
	}}

	got, err := collectOwnedBroadcastChannels(context.Background(), main, archive)
	if err != nil {
		t.Fatalf("collectOwnedBroadcastChannels: %v", err)
	}
	want := []OwnedBroadcastChannel{
		{ID: 1001, AccessHash: 2001, Title: "TDrive", CreatedAt: 100, HasActivity: true},
		{ID: 1003, AccessHash: 2003, Title: "Archive", CreatedAt: 25, HasActivity: true},
		{ID: 1002, AccessHash: 2002, Title: "TDrive", CreatedAt: 50, HasActivity: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("channels = %#v, want %#v", got, want)
	}
	if len(main.calls) != 3 || len(archive.calls) != 2 {
		t.Fatalf("query calls = main:%d archive:%d, want 3 and 2", len(main.calls), len(archive.calls))
	}
	for _, request := range append(append([]dialogs.Request{}, main.calls...), archive.calls...) {
		if request.Limit != ownedDialogsBatchSize {
			t.Fatalf("request limit = %d, want %d", request.Limit, ownedDialogsBatchSize)
		}
	}
}

func TestCollectOwnedBroadcastChannelsReturnsNoPartialResultsOnFailure(t *testing.T) {
	wantErr := errors.New("dialog lookup failed")
	query := &scriptedDialogsQuery{
		responses: []tg.MessagesDialogsClass{dialogsPage(
			dialogChannel{id: 1001, title: "TDrive", creator: true, broadcast: true, topMessage: 1},
		)},
		errAt: 2,
		err:   wantErr,
	}

	got, err := collectOwnedBroadcastChannels(context.Background(), query)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("partial channels returned after failure: %#v", got)
	}
}

func TestFakeOwnedBroadcastChannelsReturnCopies(t *testing.T) {
	fake := NewFake(7)
	fake.SeedOwnedBroadcastChannels(OwnedBroadcastChannel{ID: 1001, Title: "TDrive"})

	first, err := fake.ListOwnedBroadcastChannels(context.Background())
	if err != nil {
		t.Fatalf("first list: %v", err)
	}
	first[0].Title = "mutated"
	second, err := fake.ListOwnedBroadcastChannels(context.Background())
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if second[0].Title != "TDrive" {
		t.Fatalf("fake returned aliased state: %#v", second)
	}
}
