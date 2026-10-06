package journal

import "time"

// Run is a group of records sharing a run_id.
type Run struct {
	RunID   string
	Command string
	Start   time.Time
	End     time.Time
	Records []Record

	OK      int
	Skipped int
	Failed  int
	Freed   int64
	DryRun  bool
}

// Group folds records into runs keyed by run_id, preserving first-seen order.
func Group(records []Record) []Run {
	index := map[string]int{}
	var runs []Run
	for _, r := range records {
		i, ok := index[r.RunID]
		if !ok {
			index[r.RunID] = len(runs)
			runs = append(runs, Run{RunID: r.RunID, Command: r.Command})
			i = len(runs) - 1
		}
		run := &runs[i]
		if run.Command == "" {
			run.Command = r.Command
		}
		run.Records = append(run.Records, r)
		ts, _ := time.Parse(time.RFC3339, r.TS)
		switch r.Action {
		case ActionRunStart:
			run.Start = ts
		case ActionRunEnd:
			// The run_end frame records a summary total; the authoritative
			// freed bytes are summed from the individual item records below so
			// the frame is never double counted.
			run.End = ts
		default:
			switch r.Status {
			case StatusOK:
				run.OK++
				run.Freed += r.Bytes
			case StatusSkipped:
				run.Skipped++
			case StatusFailed:
				run.Failed++
			case StatusDryRun:
				run.DryRun = true
			}
		}
		if run.Start.IsZero() {
			run.Start = ts
		}
	}
	return runs
}

// Find returns the run with the given id.
func Find(runs []Run, id string) (Run, bool) {
	for _, r := range runs {
		if r.RunID == id {
			return r, true
		}
	}
	return Run{}, false
}

// TrashItems returns the ok trash-contract records in a run. These are the only
// records eligible for undo; owner-contract items are not restorable.
func TrashItems(run Run) []Record {
	var out []Record
	for _, r := range run.Records {
		if r.Action == ActionTrash && r.Status == StatusOK && r.Path != "" {
			out = append(out, r)
		}
	}
	return out
}

// OwnerItems returns the ok owner-contract records in a run. They are listed so
// the UI can state explicitly that they are not restorable.
func OwnerItems(run Run) []Record {
	var out []Record
	for _, r := range run.Records {
		if r.Action == ActionOwner && r.Status == StatusOK {
			out = append(out, r)
		}
	}
	return out
}
