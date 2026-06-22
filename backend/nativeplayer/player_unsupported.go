//go:build !darwin

package nativeplayer

import (
	"context"
)

type Player struct{}

func Start(ctx context.Context, url string, rect Rect, opts Options) (*Player, error) {
	return nil, ErrUnsupported
}

func (p *Player) Resize(rect Rect) error {
	return nil
}

func (p *Player) Command(command ...string) error {
	return nil
}

func (p *Player) Close() error {
	return nil
}
