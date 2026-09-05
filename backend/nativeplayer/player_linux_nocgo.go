//go:build linux && !cgo

package nativeplayer

import "context"

type Player struct{}

func Start(ctx context.Context, url string, rect Rect, opts Options) (*Player, error) {
	return nil, ErrUnsupported
}

func (p *Player) Presentation() Presentation {
	return PresentationEmbedded
}

func (p *Player) Resize(rect Rect) error {
	return nil
}

func (p *Player) ShowSeekThumbnail(_ []byte, _ Rect) error {
	return nil
}

func (p *Player) MoveSeekThumbnail(_ Rect) error {
	return nil
}

func (p *Player) HideSeekThumbnail() error {
	return nil
}

func (p *Player) Command(command ...string) error {
	return nil
}

func (p *Player) Close() error {
	return nil
}
