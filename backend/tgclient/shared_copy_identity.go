package tgclient

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
)

const sharedRawCopyPrefix = "raw:"

// SharedRawCopyIdentity identifies the exact document shown in a Shared Drive
// attachment row. An empty identity means the message cannot be copied.
func SharedRawCopyIdentity(message HistoryMessage) string {
	if message.Placeholder || message.MsgID <= 0 || message.MsgID > math.MaxInt32 || !message.HasMedia ||
		message.DocumentID <= 0 || message.MediaSize <= 0 || message.NativePhoto || message.NoForwards ||
		message.Paid || message.Restricted || message.TTLSeconds != 0 || message.AutoDeleteSeconds != 0 {
		return ""
	}
	payload := fmt.Appendf(nil, "%d/%d/%d", message.MsgID, message.DocumentID, message.MediaSize)
	return fmt.Sprintf("%s%x", sharedRawCopyPrefix, sha256.Sum256(payload))
}

// IsSharedRawCopyIdentity validates the syntax of a Shared Drive row identity.
func IsSharedRawCopyIdentity(identity string) bool {
	hash, found := strings.CutPrefix(identity, sharedRawCopyPrefix)
	if !found || len(hash) != 2*sha256.Size {
		return false
	}
	for _, char := range hash {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}
