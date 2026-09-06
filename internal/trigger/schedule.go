package trigger

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func NextFire(t Trigger, st State, now time.Time) (*time.Time, error) {
	if !t.Enabled {
		return nil, nil
	}
	loc, err := scheduleLocation(t.Schedule.Timezone)
	if err != nil {
		return nil, err
	}
	now = now.In(loc)
	var next time.Time
	switch t.Type {
	case TypeOneShot:
		if st.LastSuccessAt != nil {
			return nil, nil
		}
		next = t.Schedule.OneShotAt.In(loc)
	case TypeInterval:
		base := now
		if st.LastSuccessAt != nil {
			base = st.LastSuccessAt.In(loc)
		} else if st.LastFiredAt != nil {
			base = st.LastFiredAt.In(loc)
		}
		next = base.Add(t.Schedule.Interval)
	case TypeCron:
		next, err = nextCron(t.Schedule.Cron, now)
		if err != nil {
			return nil, err
		}
	default:
		return nil, nil
	}
	if t.Schedule.Jitter > 0 && t.Type != TypeOneShot {
		next = next.Add(deterministicJitter(t.ID, next, t.Schedule.Jitter))
	}
	return &next, nil
}

func MarkSuccess(t Trigger, st State, at time.Time) (Trigger, State) {
	st.LastSuccessAt = &at
	st.ConsecutiveFailures = 0
	if t.Type == TypeOneShot {
		t.Enabled = false
	}
	return t, st
}

func BackoffDelay(policy BackoffPolicy, failures int) (time.Duration, bool) {
	if policy.Initial <= 0 {
		policy.Initial = time.Minute
	}
	if policy.Multiplier <= 1 {
		policy.Multiplier = 2
	}
	if policy.Max <= 0 {
		policy.Max = time.Hour
	}
	if policy.MaxAttempts > 0 && failures >= policy.MaxAttempts {
		return 0, false
	}
	delay := float64(policy.Initial)
	for i := 1; i < failures; i++ {
		delay *= policy.Multiplier
	}
	d := time.Duration(delay)
	if d > policy.Max {
		d = policy.Max
	}
	return d, true
}

func ExecutionKey(triggerID string, scheduledWindow time.Time, eventHash string) string {
	h := sha256.New()
	h.Write([]byte(triggerID))
	h.Write([]byte{0})
	h.Write([]byte(scheduledWindow.UTC().Format(time.RFC3339Nano)))
	h.Write([]byte{0})
	h.Write([]byte(eventHash))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func ShouldDebounce(st State, now time.Time, window time.Duration, eventHash string) bool {
	if window <= 0 || st.LastFiredAt == nil {
		return false
	}
	return st.LastEventHash == eventHash && now.Sub(*st.LastFiredAt) < window
}

func InCooldown(st State, now time.Time) bool {
	return st.CooldownUntil != nil && st.CooldownUntil.After(now)
}

func scheduleLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	return time.LoadLocation(name)
}

func deterministicJitter(id string, at time.Time, max time.Duration) time.Duration {
	h := sha256.Sum256([]byte(id + at.UTC().Format(time.RFC3339Nano)))
	n := binary.BigEndian.Uint64(h[:8])
	return time.Duration(n % uint64(max+1))
}

func validateCron(expr string) error {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return ErrInvalidTrigger
	}
	for i, f := range fields {
		max := []int{59, 23, 31, 12, 6}[i]
		min := 0
		if i == 2 || i == 3 {
			min = 1
		}
		if f == "*" {
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil || n < min || n > max {
			return ErrInvalidTrigger
		}
	}
	return nil
}

func nextCron(expr string, now time.Time) (time.Time, error) {
	if err := validateCron(expr); err != nil {
		return time.Time{}, err
	}
	fields := strings.Fields(expr)
	next := now.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if cronMatch(fields, next) {
			return next, nil
		}
		next = next.Add(time.Minute)
	}
	return time.Time{}, ErrInvalidTrigger
}

func cronMatch(fields []string, t time.Time) bool {
	values := []int{t.Minute(), t.Hour(), t.Day(), int(t.Month()), int(t.Weekday())}
	for i, f := range fields {
		if f == "*" {
			continue
		}
		n, _ := strconv.Atoi(f)
		if values[i] != n {
			return false
		}
	}
	return true
}
