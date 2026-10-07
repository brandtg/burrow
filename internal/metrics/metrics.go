// Package metrics wraps gopsutil for the thin `status` command (spec §11).
// It is a one-shot, read-only snapshot; burrow does not monitor or daemonize.
package metrics

import (
	"context"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Process is one row of the process snapshot.
type Process struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	RSS        uint64  `json:"rss"`
	CPUPercent float64 `json:"cpu_percent"`
}

// Snapshot is a single system snapshot. All sizes are bytes.
type Snapshot struct {
	CPUPercent   float64   `json:"cpu_percent"`
	MemTotal     uint64    `json:"mem_total"`
	MemUsed      uint64    `json:"mem_used"`
	MemAvailable uint64    `json:"mem_available"`
	MemPercent   float64   `json:"mem_percent"`
	DiskPath     string    `json:"disk_path"`
	DiskTotal    uint64    `json:"disk_total"`
	DiskUsed     uint64    `json:"disk_used"`
	DiskFree     uint64    `json:"disk_free"`
	DiskPercent  float64   `json:"disk_percent"`
	NetRxBytes   uint64    `json:"net_rx_bytes"`
	NetTxBytes   uint64    `json:"net_tx_bytes"`
	ProcessCount int       `json:"process_count"`
	TopProcesses []Process `json:"top_processes"`
}

// cpuSampleInterval is a short non-zero window so the CPU figure is a current
// sample rather than the since-boot average (which is what an interval of 0
// returns).
const cpuSampleInterval = 200 * time.Millisecond

// Collect gathers a one-shot snapshot of the given filesystem root. A failure
// in one subsystem leaves that section zeroed rather than failing the whole
// snapshot; the returned error reports the first failure for the caller to log.
func Collect(ctx context.Context, root string) (Snapshot, error) {
	var snap Snapshot
	snap.DiskPath = root
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if pct, err := cpu.PercentWithContext(ctx, cpuSampleInterval, false); err == nil && len(pct) > 0 {
		snap.CPUPercent = pct[0]
	} else {
		note(err)
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		snap.MemTotal = vm.Total
		snap.MemUsed = vm.Used
		snap.MemAvailable = vm.Available
		snap.MemPercent = vm.UsedPercent
	} else {
		note(err)
	}

	if du, err := disk.UsageWithContext(ctx, root); err == nil {
		snap.DiskTotal = du.Total
		snap.DiskUsed = du.Used
		snap.DiskFree = du.Free
		snap.DiskPercent = du.UsedPercent
	} else {
		note(err)
	}

	if counters, err := gnet.IOCountersWithContext(ctx, false); err == nil && len(counters) > 0 {
		snap.NetRxBytes = counters[0].BytesRecv
		snap.NetTxBytes = counters[0].BytesSent
	} else {
		note(err)
	}

	procs, perr := process.ProcessesWithContext(ctx)
	if perr == nil {
		snap.ProcessCount = len(procs)
		snap.TopProcesses = topProcesses(ctx, procs, 5)
	} else {
		note(perr)
	}

	return snap, firstErr
}

// topProcesses returns the n processes with the largest resident set.
func topProcesses(ctx context.Context, procs []*process.Process, n int) []Process {
	out := make([]Process, 0, len(procs))
	for _, p := range procs {
		if ctx.Err() != nil {
			break
		}
		name, err := p.NameWithContext(ctx)
		if err != nil {
			continue
		}
		var rss uint64
		if mi, err := p.MemoryInfoWithContext(ctx); err == nil && mi != nil {
			rss = mi.RSS
		}
		cpuPct, _ := p.CPUPercentWithContext(ctx)
		out = append(out, Process{PID: p.Pid, Name: name, RSS: rss, CPUPercent: cpuPct})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RSS > out[j].RSS })
	if n >= 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
