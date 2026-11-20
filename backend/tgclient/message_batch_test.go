package tgclient

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

func TestChannelMessagesRequiresExactReply(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		reply   tg.MessagesMessagesClass
		wantErr bool
	}{
		{name: "exact", reply: &tg.MessagesChannelMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 7}}}},
		{name: "inexact", reply: &tg.MessagesChannelMessages{Inexact: true}, wantErr: true},
		{name: "unexpected constructor", reply: &tg.MessagesMessages{}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := tg.NewClient(invokerFunc(func(_ context.Context, _ bin.Encoder, output bin.Decoder) error {
				var buffer bin.Buffer
				if err := test.reply.Encode(&buffer); err != nil {
					return err
				}
				return output.Decode(&buffer)
			}))
			messages, err := channelMessages(t.Context(), api, InputPeer{ChannelID: 3, AccessHash: 70}, []int64{7})
			if test.wantErr {
				if err == nil || errors.Is(err, ErrMessageNotFound) || messages != nil {
					t.Fatalf("uncertain response = %v, %v", messages, err)
				}
			} else if _, placeholder := messages[7].(*tg.MessageEmpty); err != nil || !placeholder {
				t.Fatalf("exact response = %v, %v", messages, err)
			}
		})
	}
}
