package tgclient

import (
	"errors"
	"testing"

	"github.com/gotd/td/tg"
)

func TestExtractMsgIDHandlesTelegramSendVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		updates  tg.UpdatesClass
		randomID int64
		want     int64
	}{
		{
			name:     "short sent message",
			updates:  &tg.UpdateShortSentMessage{ID: 41},
			randomID: 7000,
			want:     41,
		},
		{
			name: "random id mapping",
			updates: &tg.Updates{Updates: []tg.UpdateClass{
				&tg.UpdateMessageID{ID: 42, RandomID: 7001},
			}},
			randomID: 7001,
			want:     42,
		},
		{
			name: "combined random id mapping",
			updates: &tg.UpdatesCombined{Updates: []tg.UpdateClass{
				&tg.UpdateMessageID{ID: 43, RandomID: 7002},
			}},
			randomID: 7002,
			want:     43,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := extractMsgID(test.updates, test.randomID); got != test.want {
				t.Fatalf("extractMsgID = %d, want %d", got, test.want)
			}
		})
	}
}

func TestRequiredSendMsgIDClassifiesAcceptedResponseWithoutReceiptAsUnknown(t *testing.T) {
	t.Parallel()

	msgID, err := requiredSendMsgID(&tg.Updates{}, 7003, "send control")
	if msgID != 0 || !errors.Is(err, ErrSendOutcomeUnknown) {
		t.Fatalf("requiredSendMsgID = (%d, %v), want unknown outcome", msgID, err)
	}
}
