package main

import (
	"fmt"
	"strconv"

	personaldriveservice "TDrive/backend/services/personaldrive"
)

type PersonalDriveCandidate struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CreatedAt   int64  `json:"created_at"`
	HasActivity bool   `json:"has_activity"`
	Recommended bool   `json:"recommended"`
}

type PersonalDriveSetupState struct {
	Status          string                   `json:"status"`
	ActiveChannelID string                   `json:"active_channel_id"`
	Candidates      []PersonalDriveCandidate `json:"candidates"`
}

func (a *App) personalDriveService() *personaldriveservice.Service {
	if a == nil || a.engine == nil {
		return nil
	}
	return a.engine.PersonalDriveService()
}

func (a *App) requirePersonalDriveService() (*personaldriveservice.Service, error) {
	if service := a.personalDriveService(); service != nil {
		return service, nil
	}
	return nil, fmt.Errorf("backend not ready")
}

// PreparePersonalDrive activates a valid saved drive or returns the
// creator-owned broadcast channels from which the user can explicitly choose.
func (a *App) PreparePersonalDrive() (PersonalDriveSetupState, error) {
	service, err := a.requirePersonalDriveService()
	if err != nil {
		return PersonalDriveSetupState{}, err
	}
	state, err := service.Prepare(a.ctx)
	if err != nil {
		return PersonalDriveSetupState{}, err
	}
	result := PersonalDriveSetupState{
		Status:     state.Status,
		Candidates: make([]PersonalDriveCandidate, len(state.Candidates)),
	}
	if state.ChannelID > 0 {
		result.ActiveChannelID = strconv.FormatInt(state.ChannelID, 10)
	}
	for i, candidate := range state.Candidates {
		result.Candidates[i] = PersonalDriveCandidate{
			ID:          strconv.FormatInt(candidate.ID, 10),
			Title:       candidate.Title,
			CreatedAt:   candidate.CreatedAt,
			HasActivity: candidate.HasActivity,
			Recommended: candidate.Recommended,
		}
	}
	return result, nil
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
