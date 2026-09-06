package trigger

import (
	"errors"
	"time"
)

type Type string

const (
	TypeOneShot        Type = "one_shot"
	TypeCron           Type = "cron"
	TypeInterval       Type = "interval"
	TypeEvent          Type = "event"
	TypeConditionWatch Type = "condition_watch"
	TypeManual         Type = "manual"
	TypeGoal           Type = "goal"
)

var ErrInvalidTrigger = errors.New("invalid trigger")

type Trigger struct {
	ID                 string
	ProjectID          string
	Type               Type
	Enabled            bool
	WorkflowTemplateID string
	SkillID            string
	Schedule           ScheduleSpec
	Event              EventSpec
	Condition          ConditionSpec
	PolicyRef          string
	BudgetRef          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ScheduleSpec struct {
	OneShotAt time.Time
	Interval  time.Duration
	Cron      string
	Timezone  string
	Jitter    time.Duration
}

type EventSpec struct {
	Source string
	Type   string
}

type ConditionSpec struct {
	Evaluator string
	Query     string
	Cooldown  time.Duration
}

type State struct {
	TriggerID           string
	LastFiredAt         *time.Time
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
	NextFireAt          *time.Time
	LastEventHash       string
	ConsecutiveFailures int
	CooldownUntil       *time.Time
	StateJSON           string
}

type History struct {
	ID         string
	TriggerID  string
	EventID    string
	WorkflowID string
	Status     string
	Message    string
	CreatedAt  time.Time
}

type DeadLetter struct {
	ID         string
	SourceID   string
	SourceType string
	Reason     string
	Payload    []byte
	CreatedAt  time.Time
}

type BackoffPolicy struct {
	Initial     time.Duration
	Multiplier  float64
	Max         time.Duration
	MaxAttempts int
}
