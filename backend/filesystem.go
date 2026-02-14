//go:build jsonfs
// +build jsonfs

package backend

import (
	"encoding/json"
	"math/rand"
	"time"
)

func GenerateUUID() string {
	alphanumstr := "0123456789abcdefghijklmnopqrstuvwxyz"
	alphabumslice := []int32(alphanumstr)
	UUID := ""

	for i := 0; i < 16; i++ {
		randomIndex := rand.Intn(len(alphanumstr))
		UUID = UUID + string(alphabumslice[randomIndex])
	}

	return UUID
}

func NewFileSystem() *FileSystem {
	var FS FileSystem
	FS.Folders = []Folder{}
	FS.Files = []FileMetaData{}

	return &FS
}

func (fs *FileSystem) AddFolder(name string, parentID string) Folder {
	uuid := GenerateUUID()

	folder1 := Folder{
		Name:     name,
		ParentID: parentID,
		ID:       uuid,
	}

	fs.Folders = append(fs.Folders, folder1)

	return folder1
}

func (fs *FileSystem) AddFile(name string, size int64, msgid int, pid string) FileMetaData {
	file1 := FileMetaData{
		Name:       name,
		Size:       size,
		TgMsgID:    msgid,
		ParentID:   pid,
		UploadTime: time.Now().Unix(),
	}

	fs.Files = append(fs.Files, file1)
	return file1
}

func (fs *FileSystem) ToJSON() (string, error) {
	jsondata, err := json.MarshalIndent(fs, "", " ")
	if err != nil {
		return " ", err
	}

	return string(jsondata), nil
}
