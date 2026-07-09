package livesync

import (
	"context"
	"reflect"
	"testing"

	"github.com/gotd/td/tg"
)

func TestChannelIDsFromUpdatesExtractsChannelDoorbells(t *testing.T) {
	updates := &tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateNewChannelMessage{Message: &tg.Message{PeerID: &tg.PeerChannel{ChannelID: 101}}},
			&tg.UpdateEditChannelMessage{Message: &tg.MessageService{PeerID: &tg.PeerChannel{ChannelID: 202}}},
			&tg.UpdateDeleteChannelMessages{ChannelID: 303},
			&tg.UpdateNewChannelMessage{Message: &tg.Message{PeerID: &tg.PeerChannel{ChannelID: 101}}},
		},
	}

	got := ChannelIDsFromUpdates(updates)
	want := []int64{101, 202, 303}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChannelIDsFromUpdates() = %v, want %v", got, want)
	}
}

func TestChannelIDsFromUpdatesIgnoresNonChannelUpdates(t *testing.T) {
	got := ChannelIDsFromUpdates(&tg.UpdateShort{
		Update: &tg.UpdateNewMessage{Message: &tg.Message{PeerID: &tg.PeerUser{UserID: 10}}},
	})
	if len(got) != 0 {
		t.Fatalf("ChannelIDsFromUpdates() = %v, want empty", got)
	}
}

func TestTelegramActivityHandleNeverBlocksWhenFull(t *testing.T) {
	activity := NewTelegramActivity(1)
	activity.Signal(1)

	err := activity.Handle(context.Background(), &tg.Updates{
		Updates: []tg.UpdateClass{
			&tg.UpdateDeleteChannelMessages{ChannelID: 2},
		},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if activity.Dropped() != 1 {
		t.Fatalf("Dropped() = %d, want 1", activity.Dropped())
	}
}
