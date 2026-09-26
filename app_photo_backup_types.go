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
	// RelDir is where the asset sits below its source, "/" separated, empty at
	// the top of it. A library or an album has no tree and never sets it; a
	// watched folder on a phone is enumerated by the host, so this is the only
	// thing that can say which subfolder a photo came out of.
	RelDir string `json:"rel_dir"`
	Size   int64  `json:"size"`
}

type photoBackupMaterialization struct{ path, err string }
