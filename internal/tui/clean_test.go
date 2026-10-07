package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/brandtg/burrow/internal/engine"
)

func samplePlan() *engine.Plan {
	mk := func(id, display string, size int64, rec engine.Recovery, root bool, risk engine.Risk, on bool, argv []string) *engine.PlannedItem {
		return &engine.PlannedItem{
			Target: engine.Target{
				ID: id, Category: "system", Display: display,
				SizeBytes: size, SizeKnown: true, Recovery: rec,
				NeedsRoot: root, Risk: risk, DefaultOn: on,
			},
			CategoryTitle: "System",
			Selected:      on && risk != engine.RiskHigh,
			Argv:          argv,
		}
	}
	return &engine.Plan{
		RunID:   "TESTRUN",
		Command: "clean",
		Items: []*engine.PlannedItem{
			mk("system.pkg-cache", "package cache", 1288490188, engine.RecoveryOwner, true, engine.RiskLow, true, []string{"sudo", "--", "apt-get", "clean"}),
			mk("system.journald", "systemd journal", 536870912, engine.RecoveryOwner, true, engine.RiskLow, true, []string{"sudo", "--", "journalctl", "--vacuum-size=500M"}),
			mk("system.orphans", "orphaned packages", 0, engine.RecoveryOwner, true, engine.RiskHigh, false, []string{"sudo", "--", "apt-get", "autoremove", "--purge"}),
		},
	}
}

func TestRenderPreviewGolden(t *testing.T) {
	var buf bytes.Buffer
	RenderPreview(&buf, samplePlan(), "Ubuntu 24.04 \u00b7 apt \u00b7 118.0 GiB free", false)
	got := buf.String()

	golden := filepath.Join("..", "..", "testdata", "clean", "preview.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run with UPDATE_GOLDEN=1 to create)", err)
	}
	if got != string(want) {
		t.Fatalf("preview golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderReviewListsExactArgv(t *testing.T) {
	var buf bytes.Buffer
	RenderReview(&buf, samplePlan())
	got := buf.String()
	if !bytes.Contains([]byte(got), []byte("sudo -- apt-get clean")) {
		t.Fatalf("review missing exact argv:\n%s", got)
	}
	if bytes.Contains([]byte(got), []byte("autoremove")) {
		t.Fatalf("review must not list an unselected high-risk item:\n%s", got)
	}
}

func TestRenderPermanentLabelsDelete(t *testing.T) {
	plan := &engine.Plan{
		RunID:   "TESTRUN",
		Command: "clean",
		Items: []*engine.PlannedItem{{
			Target: engine.Target{
				ID: "dev.yarn", Category: "dev", Display: "yarn cache",
				Path: "/home/u/.yarn/cache", SizeBytes: 10, SizeKnown: true,
				Recovery: engine.RecoveryTrash, Risk: engine.RiskLow, DefaultOn: true,
			},
			CategoryTitle: "Developer caches",
			Selected:      true,
			Permanent:     true,
		}},
	}
	var preview bytes.Buffer
	RenderPreview(&preview, plan, "", false)
	if !bytes.Contains(preview.Bytes(), []byte("[delete]")) {
		t.Fatalf("permanent item must be labeled [delete]:\n%s", preview.String())
	}
	if bytes.Contains(preview.Bytes(), []byte("[trash]")) {
		t.Fatalf("permanent item must not be labeled [trash]:\n%s", preview.String())
	}
	var review bytes.Buffer
	RenderReview(&review, plan)
	if !bytes.Contains(review.Bytes(), []byte("permanently delete")) {
		t.Fatalf("review must say permanently delete:\n%s", review.String())
	}
}
