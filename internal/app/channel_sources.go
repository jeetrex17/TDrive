package app

import (
	"encoding/base64"
	"fmt"

	"TDrive/backend/channelsource"
	"TDrive/backend/media"
)

func (s *DriveService) sourceService() (*channelsource.Service, error) {
	engine := s.engine()
	if engine == nil || engine.ChannelSourceService() == nil {
		return nil, errBackendUnavailable
	}
	return engine.ChannelSourceService(), nil
}

// ListChannelSourceCandidates includes joined broadcast channels in primary
// and archived Telegram dialogs. This does not create TDrive drives.
func (s *DriveService) ListChannelSourceCandidates() ([]channelsource.SourceInfo, error) {
	sources, err := s.sourceService()
	if err != nil {
		return nil, err
	}
	return sources.ListCandidates(s.host.appContext())
}

func (s *DriveService) ListConnectedChannelSources() ([]channelsource.SourceInfo, error) {
	sources, err := s.sourceService()
	if err != nil {
		return nil, err
	}
	return sources.ListConnected(s.host.appContext())
}

func (s *DriveService) ConnectChannelSource(channelID, expectedAccountID int64) (channelsource.SourceInfo, error) {
	sources, err := s.sourceService()
	if err != nil {
		return channelsource.SourceInfo{}, err
	}
	if s.connectSourceGate == nil {
		return channelsource.SourceInfo{}, errBackendUnavailable
	}
	return sources.ConnectWithGate(s.host.appContext(), channelID, expectedAccountID, s.connectSourceGate)
}

func (s *DriveService) DisconnectChannelSource(channelID, expectedAccountID int64, expectedGeneration string) error {
	sources, err := s.sourceService()
	if err != nil {
		return err
	}
	tokens, err := sources.DisconnectWithGate(s.host.appContext(), channelID, expectedAccountID, expectedGeneration, s.connectSourceGate)
	if err != nil {
		return err
	}
	if s.closeExternalMedia != nil {
		s.closeExternalMedia(tokens)
	}
	return nil
}

// ChannelSourcePhoto returns a channel's small profile photo as base64, or ""
// when the channel has none.
func (s *DriveService) ChannelSourcePhoto(channelID int64) (string, error) {
	sources, err := s.sourceService()
	if err != nil {
		return "", err
	}
	photo, err := sources.Photo(s.host.appContext(), channelID)
	if err != nil || len(photo) == 0 {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(photo), nil
}

func (s *DriveService) ListChannelMedia(channelID, offsetID int64, limit int, search, kind string) (channelsource.MediaPage, error) {
	sources, err := s.sourceService()
	if err != nil {
		return channelsource.MediaPage{}, err
	}
	return sources.Page(s.host.appContext(), channelID, offsetID, limit, search, kind)
}

// OpenChannelMedia publishes an account- and source-scoped capability. The
// lifecycle gate serializes its final publication with terminal logout.
func (s *MediaService) OpenChannelMedia(channelID, msgID, expectedAccountID int64, expectedGeneration string) (media.OpenResult, error) {
	engine := s.engine()
	if engine == nil || engine.ChannelSourceService() == nil {
		return media.OpenResult{}, errBackendUnavailable
	}
	ctx := s.host.appContext()
	release, err := s.mount.acquireMountLifecycle(ctx)
	if err != nil {
		return media.OpenResult{}, err
	}
	release()
	return engine.ChannelSourceService().OpenWithGate(ctx, channelID, msgID, expectedAccountID, expectedGeneration, func(add func() error) error {
		release, err := s.mount.acquireMountLifecycle(ctx)
		if err != nil {
			return fmt.Errorf("channel source: logout in progress: %w", err)
		}
		defer release()
		return add()
	})
}

func (s *MediaService) closeExternalNativeMedia(tokens []string) {
	for _, token := range tokens {
		_ = s.CloseNativeMedia(token)
	}
}
