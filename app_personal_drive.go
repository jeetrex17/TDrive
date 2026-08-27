package main

import (
	"fmt"
	"strconv"

	personaldriveservice "TDrive/backend/services/personaldrive"
)

// PersonalDriveCandidate is a channel the user may recover as their drive.
// The ID is a decimal string: Telegram IDs exceed JavaScript's safe integer
// range. Access hashes stay backend-only.
type PersonalDriveCandidate struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CreatedAt   int64  `json:"created_at"`
	HasActivity bool   `json:"has_activity"`
	Recommended bool   `json:"recommended"`
}

type PersonalDriveSetupState struct {
	Status          string `json:"status"`
	ActiveChannelID string `json:"active_channel_id"`
}

func (a *App) requirePersonalDriveService() (*personaldriveservice.Service, error) {
	if a == nil || a.engine == nil {
		return nil, fmt.Errorf("backend not ready")
	}
	return a.engine.PersonalDriveService(), nil
}

// PreparePersonalDrive activates the saved personal drive, or reports that
// the user has to choose one. It never contacts Telegram.
func (a *App) PreparePersonalDrive() (PersonalDriveSetupState, error) {
	service, err := a.requirePersonalDriveService()
	if err != nil {
		return PersonalDriveSetupState{}, err
	}
	state, err := service.Prepare(a.ctx)
	if err != nil {
		return PersonalDriveSetupState{}, err
	}
	result := PersonalDriveSetupState{Status: state.Status}
	if state.ChannelID > 0 {
		result.ActiveChannelID = strconv.FormatInt(state.ChannelID, 10)
	}
	return result, nil
}

// DiscoverPersonalDrives lists the broadcast channels the user created so an
// existing drive can be recovered. Read-only.
func (a *App) DiscoverPersonalDrives() ([]PersonalDriveCandidate, error) {
	service, err := a.requirePersonalDriveService()
	if err != nil {
		return nil, err
	}
	candidates, err := service.Discover(a.ctx)
	if err != nil {
		return nil, err
	}
	return personalDriveCandidates(candidates), nil
}

func (a *App) SelectPersonalDrive(channelID string) error {
	parsed, err := strconv.ParseInt(channelID, 10, 64)
	if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != channelID {
		return fmt.Errorf("invalid channel id")
	}
	service, err := a.requirePersonalDriveService()
	if err != nil {
		return err
	}
	return service.Select(a.ctx, parsed)
}

func (a *App) CreatePersonalDrive() error {
	service, err := a.requirePersonalDriveService()
	if err != nil {
		return err
	}
	return service.Create(a.ctx)
}

func personalDriveCandidates(candidates []personaldriveservice.Candidate) []PersonalDriveCandidate {
	result := make([]PersonalDriveCandidate, len(candidates))
	for i, candidate := range candidates {
		result[i] = PersonalDriveCandidate{
			ID:          strconv.FormatInt(candidate.ID, 10),
			Title:       candidate.Title,
			CreatedAt:   candidate.CreatedAt,
			HasActivity: candidate.HasActivity,
			Recommended: candidate.Recommended,
		}
	}
	return result
}
