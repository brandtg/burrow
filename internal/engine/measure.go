package engine

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/brandtg/burrow/internal/owner"
)

// Measure fills SizeBytes for targets that discovery left unknown. It is
// read-only: directory trees are measured with `du -sk` under the size-probe
// timeout, single files with a stat. A failure or timeout leaves the size
// unknown rather than reporting a false zero (spec §8.8).
func Measure(ctx context.Context, env *Env, targets []Target) {
	for i := range targets {
		t := &targets[i]
		if t.SizeKnown || t.Path == "" {
			continue
		}
		if size, ok := measurePath(ctx, env, t.Path); ok {
			t.SizeBytes = size
			t.SizeKnown = true
		}
	}
}

func measurePath(ctx context.Context, env *Env, path string) (int64, bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}
	if !fi.IsDir() {
		return diskUsage(fi), true
	}
	if env == nil || env.Run == nil {
		return 0, false
	}
	out, err := env.Run.Probe(ctx, []string{"du", "-sk", "--", path}, owner.TimeoutSizeProbe)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, false
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || kb < 0 {
		return 0, false
	}
	return kb * 1024, true
}

// diskUsage returns on-disk usage (blocks * 512) for a stat result.
func diskUsage(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return fi.Size()
}
