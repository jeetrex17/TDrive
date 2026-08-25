package mountcontroller

import (
	"context"
	"fmt"
	"sync"

	"TDrive/backend/mountfs"
)

type contentLifetimeGroup struct {
	once    sync.Once
	members []ContentLifetime
}

func (group *contentLifetimeGroup) add(content ContentLifetime) {
	if group == nil || content == nil {
		return
	}
	group.members = append(group.members, content)
}

func (group *contentLifetimeGroup) Close() {
	if group == nil {
		return
	}
	group.once.Do(func() {
		for index := len(group.members) - 1; index >= 0; index-- {
			group.members[index].Close()
		}
		group.members = nil
	})
}

func (controller *Controller) prepareSession(
	ctx context.Context,
	active *session,
) (mountfs.ReadFilesystem, error) {
	if controller == nil || active == nil || len(active.drives) == 0 {
		return nil, ErrInvalidConfiguration
	}
	personal, hasPersonal := personalDriveIn(active.drives)
	if hasPersonal && personal.Encrypted {
		if controller.keys == nil {
			return nil, ErrEncryptionPasswordRequired
		}
		lease, err := controller.keys.Acquire(ctx, personal)
		if err != nil || lease == nil {
			if err == nil {
				err = ErrEncryptionPasswordRequired
			}
			return nil, err
		}
		active.key = lease
	}

	contents := &contentLifetimeGroup{}
	active.content = contents
	filesystems := make(map[int64]*mountfs.FS, len(active.drives))
	roots := make([]mountfs.AggregateRoot, 0, len(active.drives))
	for _, drive := range active.drives {
		lease := MountKeyLease(nil)
		if drive.Kind == DriveKindPersonal {
			lease = active.key
		}
		filesystem, content, err := controller.filesystems.Build(ctx, drive.ID, controller.fsOptions, lease)
		if content != nil {
			contents.add(content)
		}
		if err != nil {
			return nil, err
		}
		if filesystem == nil || content == nil {
			return nil, fmt.Errorf("%w: filesystem builder returned incomplete resources", ErrInvalidConfiguration)
		}
		filesystems[drive.ID] = filesystem
		roots = append(roots, mountfs.AggregateRoot{
			DriveID:  drive.ID,
			Name:     aggregateRootName(drive),
			Personal: drive.Kind == DriveKindPersonal,
			FS:       filesystem,
		})
	}

	if len(active.drives) == 1 {
		filesystem := filesystems[active.drives[0].ID]
		if err := controller.prepareWriter(ctx, active, active.drives[0], filesystem, ""); err != nil {
			return nil, err
		}
		return filesystem, nil
	}

	aggregate, err := mountfs.NewAggregate(roots)
	if err != nil {
		return nil, err
	}
	if active.mode == ModeReadWrite {
		rootName, ok := aggregate.RootName(personal.ID)
		if !hasPersonal || !ok {
			return nil, ErrInvalidConfiguration
		}
		if err := controller.prepareWriter(ctx, active, personal, filesystems[personal.ID], rootName); err != nil {
			return nil, err
		}
	}
	return aggregate, nil
}

func (controller *Controller) prepareWriter(
	ctx context.Context,
	active *session,
	drive Drive,
	filesystem *mountfs.FS,
	rootName string,
) error {
	if active.mode != ModeReadWrite {
		return nil
	}
	writer, err := controller.writers.Build(ctx, drive, filesystem, active.key)
	if writer != nil {
		controller.setWriter(active, writer)
	}
	if err != nil {
		return err
	}
	if writer == nil {
		return fmt.Errorf("%w: writer builder returned no writer", ErrInvalidConfiguration)
	}
	if rootName == "" {
		return nil
	}
	delegate, ok := writer.(rootedWriteDelegate)
	if !ok {
		return fmt.Errorf("%w: aggregate writer is incomplete", ErrInvalidConfiguration)
	}
	rooted, err := newRootedWriteSession(rootName, delegate)
	if err != nil {
		return err
	}
	controller.setWriter(active, rooted)
	return nil
}

func personalDriveIn(drives []Drive) (Drive, bool) {
	for _, drive := range drives {
		if drive.Kind == DriveKindPersonal {
			return drive, true
		}
	}
	return Drive{}, false
}

func aggregateRootName(drive Drive) string {
	if drive.Kind == DriveKindPersonal {
		return "Personal"
	}
	return "Shared — " + drive.Title
}
