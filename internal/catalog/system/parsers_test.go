package system

import "testing"

func TestParseJournaldUsage(t *testing.T) {
	size, ok := parseJournaldUsage("Archived and active journals take up 512.0M in the file system.")
	if !ok || size != 512*1024*1024 {
		t.Fatalf("journald size = %d ok=%v", size, ok)
	}
}
