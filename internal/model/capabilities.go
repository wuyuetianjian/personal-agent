package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type CapabilityState string

const (
	CapabilityUnknown     CapabilityState = ""
	CapabilitySupported   CapabilityState = "supported"
	CapabilityUnsupported CapabilityState = "unsupported"
)

type Capabilities struct {
	Streaming        CapabilityState
	Tools            CapabilityState
	ParallelTools    CapabilityState
	StructuredOutput CapabilityState
	Cancel           CapabilityState
}

// Restrict permits configuration to disable support, never to claim a successful probe.
func (c Capabilities) Restrict(config Capabilities) Capabilities {
	restrict := func(actual, configured CapabilityState) CapabilityState {
		if configured == CapabilityUnsupported {
			return CapabilityUnsupported
		}
		return actual
	}
	return Capabilities{restrict(c.Streaming, config.Streaming), restrict(c.Tools, config.Tools), restrict(c.ParallelTools, config.ParallelTools), restrict(c.StructuredOutput, config.StructuredOutput), restrict(c.Cancel, config.Cancel)}
}

// CapabilityCacheKey takes a non-secret configuration fingerprint, never credentials.
func CapabilityCacheKey(endpoint, model, configurationFingerprint string) string {
	b, _ := json.Marshal([]string{endpoint, model, configurationFingerprint})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
