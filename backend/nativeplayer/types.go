package nativeplayer

import "errors"

var ErrUnsupported = errors.New("native player is not supported on this platform")
var ErrDecoderUnsafe = errors.New("native player decoder preflight failed")

type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (r Rect) Valid() bool {
	return r.Width > 0 && r.Height > 0
}
