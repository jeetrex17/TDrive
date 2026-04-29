package sync

import (
	"sort"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// ParsedMessage is a tgclient.HistoryMessage that successfully decoded into
// a TDX1 op. Messages without a header (legacy uploads, regular Telegram
// posts) are dropped during parsing; the unmanaged-bucket UI in Step 6 will
// surface them separately.
type ParsedMessage struct {
	MsgID     int64
	FromID    int64
	Op        projection.Op
	RawHeader string
}

// ParseHistoryPage filters and parses a slice of history messages. Order is
// not preserved — the caller is expected to sort the result ascending by
// MsgID before feeding it to ProjectFromOp.
func ParseHistoryPage(msgs []tgclient.HistoryMessage) []ParsedMessage {
	out := make([]ParsedMessage, 0, len(msgs))
	for _, m := range msgs {
		header := projection.ExtractHeaderLine(m.Text)
		op, err := projection.Parse(header)
		if err != nil {
			continue
		}
		if op.Type == projection.OpFileUpload || op.Type == projection.OpMeta {
			if op.FileSize == 0 && m.MediaSize > 0 {
				op.FileSize = m.MediaSize
			}
			if op.FileUploadTime == 0 && m.Date > 0 {
				op.FileUploadTime = m.Date
			}
		}
		out = append(out, ParsedMessage{
			MsgID:     m.MsgID,
			FromID:    m.FromID,
			Op:        op,
			RawHeader: header,
		})
	}
	return out
}

// SortAscending sorts by msg_id in place. Telegram history is descending;
// we always project ascending so out-of-order ops can't reach ApplyOp.
func SortAscending(msgs []ParsedMessage) {
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].MsgID < msgs[j].MsgID })
}
