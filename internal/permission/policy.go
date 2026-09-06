package permission

import (
	"errors"
	"fmt"

	"agent/internal/config"
)

var ErrUnknownDecision = errors.New("unknown permission decision")

type Policy struct {
	Default   Decision
	Actions   map[Action]Decision
	HighRisk  map[Action]bool
	RequireID bool
}

func DefaultPolicy() Policy {
	return Policy{
		Default: DecisionDeny,
		Actions: map[Action]Decision{
			ActionBrowserRead: DecisionAllow,
			ActionMemoryWrite: DecisionConfirm,
		},
		HighRisk: map[Action]bool{
			ActionBrowserHighRisk:    true,
			ActionCredentialAccess:   true,
			ActionExternalSideEffect: true,
		},
	}
}

func NewPolicyFromConfig(cfg config.PermissionsConfig) (Policy, error) {
	policy := DefaultPolicy()
	if cfg.Default != "" {
		decision, err := ParseDecision(cfg.Default)
		if err != nil {
			return Policy{}, err
		}
		policy.Default = decision
	}
	for _, rule := range cfg.Rules {
		action := Action(rule.Action)
		decision, err := ParseDecision(rule.Decision)
		if err != nil {
			return Policy{}, fmt.Errorf("permissions rule %s: %w", rule.Action, err)
		}
		if policy.Actions == nil {
			policy.Actions = make(map[Action]Decision)
		}
		policy.Actions[action] = decision
		if rule.HighRisk {
			if policy.HighRisk == nil {
				policy.HighRisk = make(map[Action]bool)
			}
			policy.HighRisk[action] = true
		}
	}
	return policy, nil
}

func ParseDecision(value string) (Decision, error) {
	switch Decision(value) {
	case DecisionAllow, DecisionDeny, DecisionConfirm:
		return Decision(value), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownDecision, value)
	}
}

func (p Policy) DecisionFor(action Action) Decision {
	if p.HighRisk[action] {
		if decision, ok := p.Actions[action]; ok && decision == DecisionAllow {
			return DecisionAllow
		}
		return DecisionConfirm
	}
	if decision, ok := p.Actions[action]; ok {
		return decision
	}
	if p.Default == "" {
		return DecisionDeny
	}
	return p.Default
}
