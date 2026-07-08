package sync

import (
	"fmt"
	"sort"
	"strings"

	"TDrive/backend/projection"
	"TDrive/backend/tgclient"
)

// ParsedMessage is a tgclient.HistoryMessage that decoded into a projectable
// op. By default only TDX1 captions are projectable. Personal-drive sync can
// opt into adopting captionless Telegram documents as root files.
type ParsedMessage struct {
	MsgID              int64
	FromID             int64
	Op                 projection.Op
	RawHeader          string
	AdoptedCaptionless bool
}

type ParseOptions struct {
	// AdoptCaptionlessMedia projects regular Telegram document messages as
	// root files. This is safe for personal drives, where the channel is the
	// user's storage bucket; shared drives intentionally leave this off so
	// normal chat attachments do not become TDrive files.
	AdoptCaptionlessMedia bool
}

// ParseHistoryPage filters and parses a slice of history messages. Order is
// not preserved — the caller is expected to sort the result ascending by
// MsgID before feeding it to ProjectFromOp.
func ParseHistoryPage(msgs []tgclient.HistoryMessage) []ParsedMessage {
	return ParseHistoryPageWithOptions(msgs, ParseOptions{})
}

func ParseHistoryPageWithOptions(msgs []tgclient.HistoryMessage, opts ParseOptions) []ParsedMessage {
	out := make([]ParsedMessage, 0, len(msgs))
	for _, m := range msgs {
		header := projection.ExtractHeaderLine(m.Text)
		op, err := projection.Parse(header)
		adoptedCaptionless := false
		if err != nil {
			if !opts.AdoptCaptionlessMedia || tdriveHeaderCandidate(header) {
				continue
			}
			var ok bool
			op, header, ok = captionlessMediaOp(m)
			if !ok {
				continue
			}
			adoptedCaptionless = true
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
			MsgID:              m.MsgID,
			FromID:             m.FromID,
			Op:                 op,
			RawHeader:          header,
			AdoptedCaptionless: adoptedCaptionless,
		})
	}
	return out
}

func tdriveHeaderCandidate(header string) bool {
	return strings.HasPrefix(strings.TrimSpace(header), "TDX1")
}

func captionlessMediaOp(m tgclient.HistoryMessage) (projection.Op, string, bool) {
	if !m.HasMedia {
		return projection.Op{}, "", false
	}
	name := strings.TrimSpace(m.DocumentName)
	if name == "" {
		name = fmt.Sprintf("Telegram file %d", m.MsgID)
	}
	op := projection.Op{
		Type:           projection.OpFileUpload,
		Parent:         projection.RootParent,
		Name:           name,
		FileSize:       m.MediaSize,
		FileUploadTime: m.Date,
	}
	return op, projection.Format(op), true
}

// SortAscending sorts by msg_id in place. Telegram history is descending;
// we always project ascending so out-of-order ops can't reach ApplyOp.
func SortAscending(msgs []ParsedMessage) {
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].MsgID < msgs[j].MsgID })
}
