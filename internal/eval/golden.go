package eval

type GoldenCase struct {
	ID       string
	Area     string
	Input    string
	Expected []string
}

func P13GoldenCases() []GoldenCase {
	return []GoldenCase{
		{ID: "local-qa", Area: "local_qa", Input: "answer from local evidence", Expected: []string{"local", "evidence"}},
		{ID: "memory-temporal", Area: "memory", Input: "what happened last time", Expected: []string{"memory", "recent"}},
		{ID: "hybrid-rag", Area: "rag", Input: "combine documents and memory", Expected: []string{"document", "memory"}},
		{ID: "planning", Area: "planning", Input: "plan a multi-step task", Expected: []string{"workflow", "step"}},
		{ID: "parallel-agents", Area: "agent", Input: "split independent work", Expected: []string{"parallel", "agent"}},
		{ID: "early-stop", Area: "verification", Input: "stop once evidence is sufficient", Expected: []string{"early", "evidence"}},
		{ID: "browser", Area: "browser", Input: "read a local page", Expected: []string{"browser", "permission"}},
		{ID: "mcp", Area: "mcp", Input: "use read-only metadata", Expected: []string{"mcp", "read"}},
		{ID: "coding", Area: "coding", Input: "prepare a patch", Expected: []string{"worktree", "diff"}},
		{ID: "public-escalation", Area: "privacy", Input: "request public model escalation", Expected: []string{"privacy", "approval"}},
		{ID: "skill-match", Area: "skill", Input: "choose a skill", Expected: []string{"skill", "version"}},
		{ID: "workflow-recovery", Area: "recovery", Input: "resume after restart", Expected: []string{"checkpoint", "resume"}},
		{ID: "proactive-watchers", Area: "trigger", Input: "fire a watcher", Expected: []string{"trigger", "dedup"}},
	}
}
