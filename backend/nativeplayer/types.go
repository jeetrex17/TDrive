package nativeplayer

import "errors"

var ErrUnsupported = errors.New("native player is not supported on this platform")
var ErrDecoderUnsafe = errors.New("native player decoder preflight failed")

type BufferedRange struct {
	Start float64
	End   float64
}

type State struct {
	Paused      bool
	CurrentTime float64
	Duration    float64
	Buffered    []BufferedRange
	Volume      float64
	Muted       bool
	Rate        float64
	Loading     bool
}

type StateHandler func(State)

type Options struct {
	UseHTMLControls bool
	OnState         StateHandler
}

type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (r Rect) Valid() bool {
	return r.Width > 0 && r.Height > 0
}
