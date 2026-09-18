package main

// PhotoBackupAsset is the native/frontend handoff shape for one discovered
// photo-library or watched-folder item.
type PhotoBackupAsset struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Name       string `json:"name"`
	MediaType  string `json:"media_type"`
	ResourceID string `json:"resource_id"`
	ModifiedAt int64  `json:"modified_at"`
	// CreatedAt is the capture time in Unix milliseconds, or 0 when the host
	// does not know it. Editing a photo moves ModifiedAt; this stays put.
	CreatedAt int64 `json:"created_at"`
	Size      int64 `json:"size"`
}

type photoBackupMaterialization struct{ path, err string }
