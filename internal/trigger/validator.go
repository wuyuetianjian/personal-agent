package trigger

func Validate(t Trigger) error {
	if t.ID == "" || t.ProjectID == "" {
		return ErrInvalidTrigger
	}
	switch t.Type {
	case TypeOneShot:
		if t.Schedule.OneShotAt.IsZero() {
			return ErrInvalidTrigger
		}
	case TypeInterval:
		if t.Schedule.Interval <= 0 {
			return ErrInvalidTrigger
		}
	case TypeCron:
		if err := validateCron(t.Schedule.Cron); err != nil {
			return err
		}
	case TypeEvent:
		if t.Event.Source == "" || t.Event.Type == "" {
			return ErrInvalidTrigger
		}
	case TypeConditionWatch:
		if t.Condition.Evaluator == "" {
			return ErrInvalidTrigger
		}
	case TypeManual, TypeGoal:
	default:
		return ErrInvalidTrigger
	}
	return nil
}
