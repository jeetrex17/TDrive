package mountcontroller

import (
	"context"
	"fmt"

	"TDrive/backend/mountcontent"
	"TDrive/backend/mountfs"
	readservice "TDrive/backend/services/read"
	"TDrive/backend/tgclient"
)

type engineFilesystemBuilder struct {
	reads  *readservice.Service
	peers  mountcontent.PeerResolver
	ranges tgclient.RangeClient
}

func (builder *engineFilesystemBuilder) Build(
	ctx context.Context,
	channelID int64,
	options mountfs.Options,
	lease MountKeyLease,
) (*mountfs.FS, ContentLifetime, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("mount: filesystem context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if builder == nil || builder.reads == nil || builder.reads.DB == nil {
		return nil, nil, fmt.Errorf("mount: database is not ready")
	}
	if builder.peers == nil || builder.ranges == nil {
		return nil, nil, fmt.Errorf("mount: Telegram range reads are unavailable")
	}

	var keys mountcontent.MasterKeyProvider
	if lease != nil {
		keys = mountcontent.MasterKeyProviderFunc(func(keyCtx context.Context, requestedChannelID int64) ([]byte, error) {
			if keyCtx == nil || requestedChannelID != channelID {
				return nil, mountcontent.ErrKeyUnavailable
			}
			if err := keyCtx.Err(); err != nil {
				return nil, err
			}
			key, err := lease.Key()
			if err != nil {
				return nil, fmt.Errorf("%w: mount key lease is unavailable", mountcontent.ErrKeyUnavailable)
			}
			return key, nil
		})
	}
	opener, err := mountcontent.New(mountcontent.Config{
		DB:     builder.reads.DB,
		Peers:  builder.peers,
		Ranges: builder.ranges,
		Keys:   keys,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("mount: initialize content reader: %w", err)
	}
	filesystem, err := mountfs.NewWithOptions(
		channelID,
		directorySource{reads: builder.reads},
		contentAdapter{opener: opener},
		options,
	)
	if err != nil {
		opener.Close()
		return nil, nil, fmt.Errorf("mount: create filesystem: %w", err)
	}
	return filesystem, opener, nil
}
