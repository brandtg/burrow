package engine

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/brandtg/burrow/internal/owner"
)

// measureWorkers bounds concurrent size probes. Each directory probe forks a
// `du -sk`, so the cap bounds both process count and disk contention.
const measureWorkers = 8

// Measure fills SizeBytes for targets that discovery left unknown. It is
// read-only: directory trees are measured with `du -sk` under the size-probe
// timeout, single files with a stat. A failure or timeout leaves the size
// unknown rather than reporting a false zero.
//
// Probes run concurrently, capped at measureWorkers, and stop promptly when ctx
// is cancelled. Each goroutine writes only its own target, so no lock is needed.
func Measure(ctx context.Context, env *Env, targets []Target) {
	var pending []*Target
	for i := range targets {
		t := &targets[i]
		if t.SizeKnown || t.Path == "" {
			continue
		}
		pending = append(pending, t)
	}
	if len(pending) == 0 {
		return
	}

	workers := measureWorkers
	if workers > len(pending) {
		workers = len(pending)
	}
	work := make(chan *Target)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				if ctx.Err() != nil {
					continue
				}
				if size, ok := measurePath(ctx, env, t.Path); ok {
					t.SizeBytes = size
					t.SizeKnown = true
				}
			}
		}()
	}
	for _, t := range pending {
		select {
		case work <- t:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(work)
	wg.Wait()
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
