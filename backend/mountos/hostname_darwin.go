//go:build darwin && !ios

package mountos

import "TDrive/backend/mountlocal"

const friendlyCapabilityPrefix = "http://" + mountlocal.Hostname + ":"

// EndpointHostname opts the production endpoint into the same exact authority
// that Finder will display. Other platforms and injected connectors stay numeric.
func (*darwinConnector) EndpointHostname() string { return mountlocal.Hostname }
