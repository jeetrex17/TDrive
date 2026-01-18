package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

var CurrentFS *FileSystem

func getLocalPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	appFolder := filepath.Join(configDir, "TDrive")
	if err := os.MkdirAll(appFolder, 0o755); err != nil {
		return "", err
	}

	return filepath.Join(appFolder, "tdrive_system.json"), nil
}

func SaveTdriveFS() error {
	if CurrentFS == nil {
		return fmt.Errorf("nothing to save")
	}

	path, err := getLocalPath()
	if err != nil {
		return err
	}

	data, err := CurrentFS.ToJSON()
	if err != nil {
		return err
	}

	return os.WriteFile(path, []byte(data), 0o644)
}

func LoadTdriveFs() error {
	path, err := getLocalPath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {

		CurrentFS = NewFileSystem()
		return SaveTdriveFS()
	} else if err != nil {
		return err
	}

	tempFS := NewFileSystem()
	err = json.Unmarshal(data, tempFS)
	if err != nil {
		return fmt.Errorf("corrupt config file: %v", err)
	}

	CurrentFS = tempFS
	return nil
}
