package metrics

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotJSONGolden pins the stable `status --json` schema. It uses a
// synthetic snapshot because a real one is not reproducible.
func TestSnapshotJSONGolden(t *testing.T) {
	snap := Snapshot{
		CPUPercent:   12.5,
		MemTotal:     16 << 30,
		MemUsed:      8 << 30,
		MemAvailable: 7 << 30,
		MemPercent:   50,
		DiskPath:     "/",
		DiskTotal:    500 << 30,
		DiskUsed:     300 << 30,
		DiskFree:     200 << 30,
		DiskPercent:  60,
		NetRxBytes:   123456,
		NetTxBytes:   654321,
		ProcessCount: 321,
		TopProcesses: []Process{{PID: 1, Name: "systemd", RSS: 10 << 20, CPUPercent: 0.1}},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	path := filepath.Join("..", "..", "testdata", "status", "golden.json")
	if os.Getenv("BURROW_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("status golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
