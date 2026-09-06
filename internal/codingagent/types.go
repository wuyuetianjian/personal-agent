package codingagent

import (
	"errors"
	"time"
)

type Capability string

const (
	CapabilityCoding     Capability = "coding"
	CapabilityCodeReview Capability = "code_review"
	CapabilityCodeTest   Capability = "code_test"
	CapabilityCodeDebug  Capability = "code_debug"
)

type ExecutionLocation string

const (
	ExecutionLocal  ExecutionLocation = "local"
	ExecutionRemote ExecutionLocation = "remote"
)

type InferenceTrust string

const (
	InferenceLocalPrivate  InferenceTrust = "local_private"
	InferenceTrustedRemote InferenceTrust = "trusted_remote"
	InferencePublicRemote  InferenceTrust = "public_remote"
)

type RepositoryPrivacy string

const (
	RepositoryPublic       RepositoryPrivacy = "public"
	RepositoryPrivate      RepositoryPrivacy = "private"
	RepositoryConfidential RepositoryPrivacy = "confidential"
)

type PrivacyPolicy string

const (
	PrivacyAllowAll         PrivacyPolicy = "allow_all"
	PrivacyDenyConfidential PrivacyPolicy = "deny_confidential"
)

var (
	ErrBackendNotFound       = errors.New("coding agent backend not found")
	ErrNoBackendAvailable    = errors.New("no coding agent backend available")
	ErrCapabilityUnsupported = errors.New("coding agent capability unsupported")
	ErrBackendDisabled       = errors.New("coding agent backend disabled")
	ErrBackendUnhealthy      = errors.New("coding agent backend unhealthy")
	ErrPrivacyDenied         = errors.New("repository privacy denies coding agent")
	ErrDirectWriteDenied     = errors.New("direct writes to source repository are denied")
	ErrInsufficientEvidence  = errors.New("coding agent result has insufficient evidence")
)

type BackendConfig struct {
	ID                string
	Enabled           bool
	Adapter           string
	Binary            string
	Timeout           time.Duration
	Concurrency       int
	ExecutionLocation ExecutionLocation
	InferenceTrust    InferenceTrust
	PrivacyPolicy     PrivacyPolicy
	Capabilities      []Capability
	AllowDirectWrites bool
}

type Request struct {
	TaskID            string
	NodeID            string
	RepositoryPath    string
	WorkspacePath     string
	Prompt            string
	Privacy           RepositoryPrivacy
	Required          []Capability
	AllowWrite        bool
	AllowDirectWrites bool
	TestCommands      [][]string
	Timeout           time.Duration
}

type Result struct {
	BackendID        string
	TaskID           string
	NodeID           string
	Summary          string
	WorkspacePath    string
	Diff             string
	FilesChanged     []string
	Commands         []CommandEvidence
	Tests            []CommandEvidence
	ExitCode         int
	TimedOut         bool
	Cancelled        bool
	EstimatedCostUSD float64
}

type CommandEvidence struct {
	Args       []string
	Dir        string
	ExitCode   int
	Stdout     string
	Stderr     string
	StartedAt  time.Time
	FinishedAt time.Time
	TimedOut   bool
	Cancelled  bool
}

func (r Result) ValidateEvidence() error {
	if r.Diff == "" && len(r.FilesChanged) == 0 && len(r.Tests) == 0 {
		return ErrInsufficientEvidence
	}
	return nil
}
