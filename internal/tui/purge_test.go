package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/purge"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

func sampleArtifacts(t *testing.T) []purge.Artifact {
	t.Helper()
	base := t.TempDir()
	mk := func(project, name string, kept bool, reason string) purge.Artifact {
		dir := filepath.Join(base, project, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		snap, err := safe.TakeSnapshot(dir)
		if err != nil {
			t.Fatal(err)
		}
		return purge.Artifact{
			Path:       dir,
			Type:       name,
			Project:    filepath.Join(base, project),
			Size:       1024,
			SizeKnown:  true,
			Mtime:      time.Now().Add(-48 * time.Hour),
			AgeKnown:   true,
			KeepReason: reason,
			Snapshot:   snap,
		}
	}
	return []purge.Artifact{
		mk("alpha", "node_modules", false, ""),
		mk("alpha", "target", false, ""),
		mk("beta", "build", true, "git-tracked"),
	}
}

func TestPurgeModelGrouping(t *testing.T) {
	arts := sampleArtifacts(t)
	m := NewPurgeModel(PurgeConfig{Artifacts: arts, Now: time.Now()})
	headers := 0
	for _, r := range m.rows {
		if r.art == nil {
			headers++
		}
	}
	if headers != 2 {
		t.Fatalf("expected 2 project headers, got %d", headers)
	}
}

func TestPurgeModelSelectOnlyPurgeable(t *testing.T) {
	arts := sampleArtifacts(t)
	m := NewPurgeModel(PurgeConfig{Artifacts: arts, Now: time.Now()})
	m.selectAll()
	if m.SelectedCount() != 2 {
		t.Fatalf("selectAll selected %d, want 2 (kept item excluded)", m.SelectedCount())
	}
}

func TestPurgeModelToggleGroup(t *testing.T) {
	arts := sampleArtifacts(t)
	m := NewPurgeModel(PurgeConfig{Artifacts: arts, Now: time.Now()})
	// Cursor starts on the first header row.
	if m.rows[m.cursor].art != nil {
		t.Fatalf("expected cursor on a header, got artifact")
	}
	m.toggle()
	if m.SelectedCount() != 2 {
		t.Fatalf("group toggle selected %d, want 2", m.SelectedCount())
	}
	m.toggle()
	if m.SelectedCount() != 0 {
		t.Fatalf("group toggle-off left %d selected", m.SelectedCount())
	}
}

func TestPurgeModelJumpGroup(t *testing.T) {
	arts := sampleArtifacts(t)
	m := NewPurgeModel(PurgeConfig{Artifacts: arts, Now: time.Now()})
	m.jumpGroup(1)
	if m.rows[m.cursor].art != nil {
		t.Fatalf("jumpGroup should land on a header")
	}
}

func TestPurgeModelExecuteDeletes(t *testing.T) {
	arts := sampleArtifacts(t)
	exec := &purge.Executor{
		Validator: safe.NewValidator(os.Getuid()),
		Trash:     trash.New(filepath.Join(t.TempDir(), "Trash"), os.Getuid()),
	}
	m := NewPurgeModel(PurgeConfig{Artifacts: arts, Now: time.Now(), Executor: exec})
	m.selectAll()
	victim := arts[0].Path
	m.execute()
	if _, err := os.Lstat(victim); !os.IsNotExist(err) {
		t.Fatalf("selected artifact should be deleted, stat err = %v", err)
	}
	if m.freed <= 0 {
		t.Fatalf("expected freed bytes, got %d", m.freed)
	}
	if _, err := os.Lstat(arts[2].Path); err != nil {
		t.Fatalf("kept artifact must survive: %v", err)
	}
}
