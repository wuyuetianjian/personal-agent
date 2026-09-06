package browser

func CoordinateFallback(action Action, cause error) (Action, error) {
	if !hasCoordinates(action.Target) {
		return Action{}, ErrCoordinateUnavailable
	}
	action.Mode = ControlModeCoordinate
	if action.FallbackReason == "" && cause != nil {
		action.FallbackReason = cause.Error()
	}
	if action.FallbackReason == "" {
		action.FallbackReason = "semantic target unavailable"
	}
	return action, nil
}

func hasCoordinates(target Target) bool {
	return target.X > 0 || target.Y > 0
}
