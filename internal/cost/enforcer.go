package cost

type Enforcer struct{}

func (Enforcer) Check(budget Budget, next Usage) LimitResult {
	nextInput := budget.CurrentInputTokens + next.InputTokens
	nextOutput := budget.CurrentOutputTokens + next.OutputTokens
	nextCost := budget.CurrentCostUSD + next.EstimatedCostUSD
	result := LimitResult{Status: LimitOK}
	if budget.MaxInputTokens > 0 && nextInput > budget.MaxInputTokens {
		result.Status = LimitHardHit
		result.Reasons = append(result.Reasons, "input token budget exceeded")
	}
	if budget.MaxOutputTokens > 0 && nextOutput > budget.MaxOutputTokens {
		result.Status = LimitHardHit
		result.Reasons = append(result.Reasons, "output token budget exceeded")
	}
	if budget.HardLimitUSD > 0 && nextCost > budget.HardLimitUSD {
		result.Status = LimitHardHit
		result.Reasons = append(result.Reasons, "hard cost budget exceeded")
	}
	if result.Status == LimitHardHit {
		return result
	}
	if budget.SoftLimitUSD > 0 && nextCost > budget.SoftLimitUSD {
		result.Status = LimitSoftHit
		result.Reasons = append(result.Reasons, "soft cost budget exceeded")
	}
	return result
}

func ApplyUsage(budget Budget, usage Usage) Budget {
	budget.CurrentInputTokens += usage.InputTokens
	budget.CurrentOutputTokens += usage.OutputTokens
	budget.CurrentCostUSD += usage.EstimatedCostUSD
	return budget
}
