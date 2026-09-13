package daemon

import (
	"TDrive/backend/core"
	"encoding/json"
	"fmt"
	"path"
)

func folderEntry(id string, folderPath string) Entry {
	name := ""
	if folderPath != "/" {
		name = path.Base(folderPath)
	}
	return Entry{Type: "folder", ID: id, Name: name, Path: folderPath}
}

func entryFromResolved(entry core.ResolvedEntry) Entry {
	return Entry{
		Type:       entry.Type,
		ID:         entry.ID,
		MsgID:      entry.MsgID,
		Name:       entry.Name,
		Path:       entry.Path,
		Size:       entry.Size,
		UploadTime: entry.UploadTime,
		Encrypted:  entry.Encrypted,
	}
}

func decodePayload(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("invalid request payload: %w", err)
	}
	return nil
}
