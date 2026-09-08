package trigger

import (
	"encoding/json"
	"time"
)

func RecordConditionCheck(st State, current bool, at time.Time) State {
	previousValue := st.CurrentState
	if previousValue == "" {
		previousValue = conditionStateFromJSON(st.StateJSON)
	}
	currentValue := conditionStateString(current)
	st.PreviousState = previousValue
	st.CurrentState = currentValue
	st.LastCheckAt = &at
	st.StateJSON = conditionStateJSON(current)
	if previousValue != "" && previousValue != currentValue {
		st.LastTransitionAt = &at
	}
	return st
}

func MarkConditionNotification(st State, at time.Time) State {
	st.LastNotificationAt = &at
	return st
}

func conditionStateString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func conditionStateJSON(value bool) string {
	raw, _ := json.Marshal(map[string]bool{"condition": value})
	return string(raw)
}

func conditionStateFromJSON(raw string) string {
	var decoded struct {
		Condition bool `json:"condition"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return ""
	}
	return conditionStateString(decoded.Condition)
}
