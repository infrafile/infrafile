package audit

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type IssueState struct {
	Identity  IssueIdentity `json:"identity"`
	Lifecycle string        `json:"lifecycle"`
	// Resume preserves the last applicable resolution while the declaration is out of scope.
	Resume        string  `json:"last_in_scope_state"`
	Assessment    string  `json:"assessment"`
	Waiver        *Waiver `json:"waiver"`
	FirstSeen     string  `json:"first_seen"`
	LastConfirmed string  `json:"last_confirmed,omitempty"`
	LastChecked   string  `json:"last_checked"`
	RecoveredAt   string  `json:"recovered_at,omitempty"`
	Occurrences   int     `json:"occurrences"`
}

type IssueChange struct {
	Identity IssueIdentity `json:"identity"`
	Type     string        `json:"type"`
	At       string        `json:"at"`
}

// AdvanceIssues is pure: callers explicitly publish its result in T09. No missing
// evaluation, waiver, unknown result or scope exit is interpreted as recovery.
func AdvanceIssues(ctx context.Context, previous []IssueState, evaluations []IssueEvaluation, at time.Time) ([]IssueState, []IssueChange, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if at.IsZero() || at.Year() < 1 || at.Year() > 9999 {
		return nil, nil, fmt.Errorf("invalid evaluation time")
	}
	stamp := at.UTC().Format(time.RFC3339)
	states := map[string]IssueState{}
	for _, state := range previous {
		if err := validateIssueState(state, at); err != nil {
			return nil, nil, err
		}
		key := state.Identity.storageKey()
		if _, ok := states[key]; ok {
			return nil, nil, fmt.Errorf("repeated historical issue")
		}
		states[key] = state
	}
	changes := []IssueChange{}
	seen := map[string]bool{}
	for _, evaluation := range evaluations {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if err := evaluation.Identity.Validate(); err != nil {
			return nil, nil, err
		}
		if evaluation.Result != "confirmed" && evaluation.Result != "clear" && evaluation.Result != "unknown" || !evaluation.InScope && evaluation.Result != "unknown" || evaluation.Result == "confirmed" && !evaluation.Found || evaluation.Result == "clear" && evaluation.Found {
			return nil, nil, fmt.Errorf("inconsistent issue evaluation")
		}
		key := evaluation.Identity.storageKey()
		if seen[key] {
			return nil, nil, fmt.Errorf("repeated issue evaluation")
		}
		seen[key] = true
		old, exists := states[key]
		state := old
		change := ""
		if !exists {
			if !evaluation.Found || !evaluation.InScope {
				continue
			}
			state = IssueState{Identity: evaluation.Identity, Lifecycle: "open", Resume: "open", FirstSeen: stamp}
			change = "unconfirmed"
			if evaluation.Result == "confirmed" {
				change = "opened"
			}
		}
		state.LastChecked = stamp
		state.Assessment = evaluation.Result
		state.Waiver = evaluation.Waiver
		if !evaluation.InScope {
			state.Lifecycle = "out_of_scope"
			if old.Lifecycle != "out_of_scope" {
				change = "out_of_scope"
			}
		} else {
			state.Lifecycle = state.Resume
			if old.Lifecycle == "out_of_scope" {
				change = "returned"
			}
			switch evaluation.Result {
			case "confirmed":
				if state.Resume == "recovered" {
					if state.Occurrences == int(^uint(0)>>1) {
						return nil, nil, fmt.Errorf("issue occurrence limit reached")
					}
					state.Occurrences++
					change = "reopened"
				} else if state.Occurrences == 0 {
					state.Occurrences = 1
					if exists {
						change = "confirmed"
					}
				}
				state.Lifecycle, state.Resume = "open", "open"
				state.LastConfirmed = stamp
			case "clear":
				if evaluation.Waiver == nil && state.Resume == "open" {
					state.Lifecycle, state.Resume = "recovered", "recovered"
					state.RecoveredAt = stamp
					change = "recovered"
				}
			}
		}
		if exists && change == "" {
			if old.Assessment != state.Assessment {
				change = state.Assessment
			}
			if old.Waiver == nil && state.Waiver != nil {
				change = "waived"
			}
			if old.Waiver != nil && state.Waiver == nil {
				change = "unwaived"
			}
		}
		states[key] = state
		if change != "" {
			changes = append(changes, IssueChange{state.Identity, change, stamp})
		}
	}
	// Omitted evaluations retain the issue as unknown; they cannot stand in for clear evidence.
	for key, state := range states {
		if !seen[key] {
			if state.Assessment != "unknown" {
				changes = append(changes, IssueChange{state.Identity, "unknown", stamp})
			}
			state.Assessment = "unknown"
			state.LastChecked = stamp
			states[key] = state
		}
	}
	out := []IssueState{}
	for _, state := range states {
		out = append(out, state)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Identity.storageKey() < out[b].Identity.storageKey() })
	sort.Slice(changes, func(a, b int) bool { return changes[a].Identity.storageKey() < changes[b].Identity.storageKey() })
	return out, changes, nil
}

func validateIssueState(state IssueState, at time.Time) error {
	if err := state.Identity.Validate(); err != nil {
		return err
	}
	if state.Resume != "open" && state.Resume != "recovered" || state.Lifecycle != "out_of_scope" && state.Lifecycle != state.Resume || state.Assessment != "confirmed" && state.Assessment != "clear" && state.Assessment != "unknown" || state.Occurrences < 0 {
		return fmt.Errorf("invalid issue state")
	}
	first, err := time.Parse(time.RFC3339, state.FirstSeen)
	if err != nil || first.After(at) {
		return fmt.Errorf("invalid issue time")
	}
	checked, err := time.Parse(time.RFC3339, state.LastChecked)
	if err != nil || checked.Before(first) || checked.After(at) {
		return fmt.Errorf("invalid issue time order")
	}
	for _, value := range []string{state.LastConfirmed, state.RecoveredAt} {
		if value == "" {
			continue
		}
		stamp, err := time.Parse(time.RFC3339, value)
		if err != nil || stamp.Before(first) || stamp.After(checked) {
			return fmt.Errorf("invalid historical issue time")
		}
	}
	if state.Occurrences > 0 && state.LastConfirmed == "" || state.Occurrences == 0 && state.LastConfirmed != "" || state.Resume == "recovered" && state.RecoveredAt == "" {
		return fmt.Errorf("invalid issue confirmation history")
	}
	if state.Resume == "recovered" && state.LastConfirmed != "" {
		confirmed, _ := time.Parse(time.RFC3339, state.LastConfirmed)
		recovered, _ := time.Parse(time.RFC3339, state.RecoveredAt)
		if recovered.Before(confirmed) {
			return fmt.Errorf("recovery precedes confirmation")
		}
	}
	return nil
}
