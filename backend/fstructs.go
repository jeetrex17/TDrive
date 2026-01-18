package backend

type Folder struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
}

type FileMetaData struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	TgMsgID    int    `json:"msg_id"`
	ParentID   string `json:"parent_id"`
	UploadTime int64  `json:"upload_time"`
}

type FileSystem struct {
	Folders []Folder       `json:"folders"`
	Files   []FileMetaData `json:"files"`
}
