//go:build (!darwin && !ios && !android && !windows) || ((darwin || ios || android) && !cgo)

package biometric

import "context"

type unsupportedProvider struct{}

func NewProvider(string) Provider { return unsupportedProvider{} }

func (unsupportedProvider) Status(context.Context, Scope) (Status, error) {
	return Status{Reason: "unsupported"}, nil
}
func (unsupportedProvider) Enroll(context.Context, Scope, []byte) error   { return ErrUnavailable }
func (unsupportedProvider) Unlock(context.Context, Scope) ([]byte, error) { return nil, ErrUnavailable }
func (unsupportedProvider) Delete(context.Context, Scope) error           { return nil }
