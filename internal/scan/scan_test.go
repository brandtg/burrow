package scan_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brandtg/burrow/internal/scan"
)

func TestAnalyzeFixture(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(root, "big.bin")
	if err := os.WriteFile(big, make([]byte, 64*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "small.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	node, err := scan.Default().Scan(context.Background(), root, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !node.IsDir {
		t.Fatal("root should be a directory")
	}
	if node.Size <= 0 {
		t.Fatalf("expected positive on-disk size, got %d", node.Size)
	}
	if node.Apparent < 64*1024 {
		t.Fatalf("expected apparent size >= 64KiB, got %d", node.Apparent)
	}
	names := map[string]bool{}
	for _, c := range node.Children {
		names[c.Name] = true
	}
	if !names["big.bin"] || !names["sub"] {
		t.Fatalf("missing expected children: %v", names)
	}
}

func TestAnalyzeCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scan.Default().Scan(ctx, t.TempDir(), scan.Options{}); err == nil {
		t.Fatal("expected canceled context to fail")
	}
}
