package app

import (
	"context"

	"TDrive/backend/channelsource"
)

func (s *DriveService) ListSourceGroups(expectedAccountID int64) (channelsource.SourceOrganization, error) {
	return s.withSourceOrganization(func(ctx context.Context, sources *channelsource.Service) (channelsource.SourceOrganization, error) {
		return sources.ListSourceGroups(ctx, expectedAccountID)
	})
}

func (s *DriveService) SaveSourceGroup(expectedAccountID int64, groupID, name string) (channelsource.SourceOrganization, error) {
	return s.withSourceOrganization(func(ctx context.Context, sources *channelsource.Service) (channelsource.SourceOrganization, error) {
		return sources.SaveSourceGroup(ctx, expectedAccountID, groupID, name)
	})
}

func (s *DriveService) DeleteSourceGroup(expectedAccountID int64, groupID string) (channelsource.SourceOrganization, error) {
	return s.withSourceOrganization(func(ctx context.Context, sources *channelsource.Service) (channelsource.SourceOrganization, error) {
		return sources.DeleteSourceGroup(ctx, expectedAccountID, groupID)
	})
}

func (s *DriveService) MoveSourceToGroup(peerKind string, peerID, expectedAccountID int64, expectedGeneration, groupID string) (channelsource.SourceOrganization, error) {
	return s.withSourceOrganization(func(ctx context.Context, sources *channelsource.Service) (channelsource.SourceOrganization, error) {
		return sources.MoveSourceToGroup(ctx, peerKind, peerID, expectedAccountID, expectedGeneration, groupID)
	})
}

func (s *DriveService) withSourceOrganization(run func(context.Context, *channelsource.Service) (channelsource.SourceOrganization, error)) (channelsource.SourceOrganization, error) {
	sources, err := s.sourceService()
	if err != nil {
		return channelsource.SourceOrganization{}, err
	}
	if s.connectSourceGate == nil {
		return channelsource.SourceOrganization{}, errBackendUnavailable
	}
	var result channelsource.SourceOrganization
	err = s.connectSourceGate(func() error {
		var err error
		result, err = run(s.host.appContext(), sources)
		return err
	})
	return result, err
}
