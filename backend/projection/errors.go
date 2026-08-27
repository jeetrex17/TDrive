package projection

import "errors"

// ErrControlProjection marks a control message that Telegram accepted but the
// local projection could not apply. Callers must stop dependent writes until a
// sync replays the accepted message into the local cache.
var ErrControlProjection = errors.New("control message accepted but local projection failed")
