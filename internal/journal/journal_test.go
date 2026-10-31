package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecordValidate(t *testing.T) {
	valid := Record{
		TS:      "2026-10-05T12:00:00Z",
		RunID:   "01J",
		Command: "clean",
		Item:    "dev.npm",
		Action:  ActionOwner,
		Status:  StatusOK,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid: %v", err)
	}
	bad := []Record{
		{},
		{TS: valid.TS, RunID: valid.RunID, Command: valid.Command, Action: valid.Action, Status: "bogus"},
		{TS: "not-a-time", RunID: valid.RunID, Command: valid.Command, Action: valid.Action, Status: StatusOK},
		{TS: valid.TS, Command: valid.Command, Action: valid.Action, Status: StatusOK},
	}
	for i, r := range bad {
		if err := r.Validate(); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

func TestWriterReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "operations.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	runID := NewRunID()
	if err := w.Append(Record{RunID: runID, Command: "clean", Action: ActionRunStart, Status: StatusStart}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Record{RunID: runID, Command: "clean", Item: "dev.npm", Action: ActionOwner, Status: StatusOK, Bytes: 42}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Record{RunID: runID, Command: "clean", Action: ActionRunEnd, Status: StatusEnd, Bytes: 42}); err != nil {
		t.Fatal(err)
	}

	records, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	runs := Group(records)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].RunID != runID || runs[0].OK != 1 || runs[0].Freed != 42 {
		t.Fatalf("unexpected run: %+v", runs[0])
	}
	if runs[0].Command != "clean" {
		t.Fatalf("command = %q", runs[0].Command)
	}
}

func TestWriterFailureIsNonFatalAndReported(t *testing.T) {
	// Point the journal at a path whose parent is a file, so creation fails.
	base := t.TempDir()
	blocker := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "operations.jsonl")
	var reported error
	w := &Writer{path: path, OnError: func(err error) { reported = err }}
	if err := w.Append(Record{RunID: "r", Command: "clean", Item: "x", Action: ActionTrash, Status: StatusOK}); err == nil {
		t.Fatal("expected append error")
	}
	if reported == nil {
		t.Fatal("expected OnError to be called")
	}
}

func TestReadMissingFile(t *testing.T) {
	records, err := Read(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("expected no records, got %d", len(records))
	}
}

func TestGroupMultipleRuns(t *testing.T) {
	records := []Record{
		{RunID: "a", Command: "clean", Action: ActionRunStart, Status: StatusStart, TS: "2026-10-05T12:00:00Z"},
		{RunID: "a", Command: "clean", Action: ActionTrash, Status: StatusOK, TS: "2026-10-05T12:00:01Z", Bytes: 10, Path: "/x"},
		{RunID: "b", Command: "purge", Action: ActionRunStart, Status: StatusStart, TS: "2026-10-05T13:00:00Z"},
		{RunID: "b", Command: "purge", Action: ActionOwner, Status: StatusFailed, TS: "2026-10-05T13:00:01Z"},
	}
	runs := Group(records)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if runs[0].RunID != "a" || runs[1].RunID != "b" {
		t.Fatalf("run order: %s, %s", runs[0].RunID, runs[1].RunID)
	}
	if runs[1].Failed != 1 {
		t.Fatalf("expected 1 failure in run b")
	}
}

func TestTrashItemsExcludesOwner(t *testing.T) {
	run := Run{Records: []Record{
		{Action: ActionTrash, Status: StatusOK, Path: "/a"},
		{Action: ActionOwner, Status: StatusOK, Path: "/b"},
		{Action: ActionTrash, Status: StatusSkipped, Path: "/c"},
	}}
	trashItems := TrashItems(run)
	if len(trashItems) != 1 || trashItems[0].Path != "/a" {
		t.Fatalf("unexpected trash items: %+v", trashItems)
	}
	ownerItems := OwnerItems(run)
	if len(ownerItems) != 1 || ownerItems[0].Path != "/b" {
		t.Fatalf("unexpected owner items: %+v", ownerItems)
	}
}
