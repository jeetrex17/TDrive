package tgclient

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const maxIdempotencyKeyBytes = 256

var fallbackRandomCounter atomic.Uint64

// StableRandomID maps a durable operation id and a namespaced step to the
// positive 63-bit random_id Telegram uses to deduplicate message sends. The
// operation id must be persisted by the caller before attempting the send.
func StableRandomID(operationID, step string) (int64, error) {
	if err := validateIdempotencyKey("operation id", operationID); err != nil {
		return 0, err
	}
	if err := validateIdempotencyKey("operation step", step); err != nil {
		return 0, err
	}

	digest := sha256.Sum256([]byte("tdrive.telegram.random-id.v1\x00" + operationID + "\x00" + step))
	id := int64(binary.BigEndian.Uint64(digest[:8]) & uint64(^uint64(0)>>1))
	if id == 0 {
		id = 1
	}
	return id, nil
}

func validateIdempotencyKey(label, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("tgclient: %s must be non-empty without surrounding whitespace", label)
	}
	if len(value) > maxIdempotencyKeyBytes {
		return fmt.Errorf("tgclient: %s exceeds %d bytes", label, maxIdempotencyKeyBytes)
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("tgclient: %s contains invalid text", label)
	}
	return nil
}

func randomID() int64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		id := int64(binary.BigEndian.Uint64(raw[:]) & uint64(^uint64(0)>>1))
		if id != 0 {
			return id
		}
	}

	// random_id is an idempotency nonce, not a secret. A process-local counter
	// prevents collisions between calls if the OS RNG temporarily fails.
	fallback := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", time.Now().UnixNano(), fallbackRandomCounter.Add(1))))
	id := int64(binary.BigEndian.Uint64(fallback[:8]) & uint64(^uint64(0)>>1))
	if id == 0 {
		return 1
	}
	return id
}
