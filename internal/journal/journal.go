// Package journal implements the append-only JSONL operation journal and the
// history reader. It is the audit trail and the source of truth for undo.
//
// Provenance: independent reimplementation of Mole's operations-log idea, with
// a stricter, schema-stable record.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Status values for a record.
const (
	StatusOK      = "ok"
	StatusSkipped = "skipped"
	StatusFailed  = "failed"
	StatusDryRun  = "dry-run"
	StatusStart   = "start"
	StatusEnd     = "end"
)

// Action values. run_start/run_end frame a run; trash/owner/purge/delete
// describe a mutation. purge and delete are permanent; delete is a trash-contract
// item removed via --permanent.
const (
	ActionTrash    = "trash"
	ActionOwner    = "owner"
	ActionPurge    = "purge"
	ActionDelete   = "delete"
	ActionRunStart = "run_start"
	ActionRunEnd   = "run_end"
)

// Recovery values mirror engine.Recovery so the journal can state the contract
// that applied to each mutation (invariant I2). They are strings here to keep
// the journal schema self-describing and free of engine imports.
const (
	RecoveryTrash  = "trash"
	RecoveryOwner  = "owner"
	RecoveryDelete = "delete"
)

// Version is stamped at build time with -ldflags.
var Version = "0.1.0-dev"

// Record is one journal line (spec §12).
type Record struct {
	TS            string   `json:"ts"`
	RunID         string   `json:"run_id"`
	Command       string   `json:"command"`
	Item          string   `json:"item"`
	Action        string   `json:"action"`
	Recovery      string   `json:"recovery,omitempty"`
	Argv          []string `json:"argv,omitempty"`
	Path          string   `json:"path,omitempty"`
	Status        string   `json:"status"`
	Bytes         int64    `json:"bytes"`
	Reason        string   `json:"reason,omitempty"`
	Host          string   `json:"host"`
	BurrowVersion string   `json:"burrow_version"`
}

// Validate checks the required fields of a record.
func (r Record) Validate() error {
	if r.TS == "" {
		return errors.New("journal: missing ts")
	}
	if _, err := time.Parse(time.RFC3339, r.TS); err != nil {
		return fmt.Errorf("journal: bad ts: %w", err)
	}
	if r.RunID == "" {
		return errors.New("journal: missing run_id")
	}
	if r.Command == "" {
		return errors.New("journal: missing command")
	}
	if r.Action == "" {
		return errors.New("journal: missing action")
	}
	switch r.Status {
	case StatusOK, StatusSkipped, StatusFailed, StatusDryRun, StatusStart, StatusEnd:
	default:
		return fmt.Errorf("journal: bad status %q", r.Status)
	}
	return nil
}

// NewRunID returns a new ULID string.
func NewRunID() string {
	return ulid.Make().String()
}

// Writer appends records to a JSONL file. A write failure is reported to the
// caller and to OnError, but never aborts a run.
type Writer struct {
	path string
	mu   sync.Mutex

	// OnError, if set, is called for every write failure.
	OnError func(error)
}

// NewWriter returns a writer for path. The parent directory is created.
func NewWriter(path string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Writer{path: path}, nil
}

// Path returns the journal file path.
func (w *Writer) Path() string { return w.path }

// Append writes one record. A failure is non-fatal by design.
func (w *Writer) Append(r Record) error {
	if r.TS == "" {
		r.TS = time.Now().UTC().Format(time.RFC3339)
	}
	if r.Host == "" {
		r.Host = Hostname()
	}
	if r.BurrowVersion == "" {
		r.BurrowVersion = Version
	}
	if err := r.Validate(); err != nil {
		w.fail(err)
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		w.fail(err)
		return err
	}
	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		w.fail(err)
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		w.fail(err)
		return err
	}
	return nil
}

func (w *Writer) fail(err error) {
	if w.OnError != nil {
		w.OnError(err)
	}
}

// StartRun writes a run_start frame and returns the new run id.
func (w *Writer) StartRun(command string) (string, error) {
	id := NewRunID()
	return id, w.Append(Record{
		RunID:   id,
		Command: command,
		Action:  ActionRunStart,
		Status:  StatusStart,
	})
}

// EndRun writes a run_end frame.
func (w *Writer) EndRun(runID, command string, freedBytes int64) error {
	return w.Append(Record{
		RunID:   runID,
		Command: command,
		Action:  ActionRunEnd,
		Status:  StatusEnd,
		Bytes:   freedBytes,
	})
}

// Hostname returns the short hostname, or "" on failure.
func Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// Read parses all records from path. A missing file yields no records.
func Read(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return out, fmt.Errorf("journal: bad line: %w", err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}
