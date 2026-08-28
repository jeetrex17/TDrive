package tgclient

import (
	"testing"

	"github.com/gotd/td/tg"
)

// Telegram returns service events and deletion stubs alongside real messages,
// and they count against the page limit it was given. Reporting them keeps a
// page's length equal to what Telegram actually sent, so a caller paging on
// page size cannot mistake a thinned page for the end of the channel.
func TestPlaceholderMessageReportsIDsThatOccupyHistorySlots(t *testing.T) {
	for _, tc := range []struct {
		name     string
		msg      tg.MessageClass
		wantID   int64
		wantDate int64
		wantOK   bool
	}{
		{"service event", &tg.MessageService{ID: 1942, Date: 1787832319}, 1942, 1787832319, true},
		{"deleted message stub", &tg.MessageEmpty{ID: 2403}, 2403, 0, true},
		{"real message is not a placeholder", &tg.Message{ID: 2458}, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, date, ok := placeholderMessage(tc.msg)
			if ok != tc.wantOK || id != tc.wantID || date != tc.wantDate {
				t.Fatalf("placeholderMessage() = (%d, %d, %v), want (%d, %d, %v)",
					id, date, ok, tc.wantID, tc.wantDate, tc.wantOK)
			}
		})
	}
}
