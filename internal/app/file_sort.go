package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"TDrive/backend/datadir"
)

const fileSortPreferenceFilename = "file-sort.json"

type FileSortPreference struct {
	Key       string `json:"key"`
	Direction string `json:"direction"`
}

func (p FileSortPreference) valid() bool {
	switch p.Key {
	case "name", "date", "size", "type":
		return p.Direction == "asc" || p.Direction == "desc"
	default:
		return false
	}
}

func (a *App) GetFileSortPreference() (*FileSortPreference, error) {
	dir, err := datadir.Dir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, fileSortPreferenceFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read file sort preference: %w", err)
	}
	var preference FileSortPreference
	if err := json.Unmarshal(data, &preference); err != nil {
		return nil, fmt.Errorf("decode file sort preference: %w", err)
	}
	if !preference.valid() {
		return nil, errors.New("invalid file sort preference")
	}
	return &preference, nil
}

func (a *App) SetFileSortPreference(preference FileSortPreference) error {
	if !preference.valid() {
		return errors.New("invalid file sort preference")
	}
	dir, err := datadir.Dir()
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".file-sort-*")
	if err != nil {
		return fmt.Errorf("create file sort preference: %w", err)
	}
	defer os.Remove(file.Name())
	writeErr := json.NewEncoder(file).Encode(preference)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("write file sort preference: %w", err)
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, fileSortPreferenceFilename)); err != nil {
		return fmt.Errorf("replace file sort preference: %w", err)
	}
	return nil
}
