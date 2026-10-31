package owner

import "time"

// Central timeout table. Every external command takes its deadline
// from here so behavior is uniform and reviewable.
const (
	TimeoutQuickDetect = 5 * time.Second
	TimeoutSizeProbe   = 30 * time.Second
	TimeoutOwnerClean  = 10 * time.Minute
	TimeoutScanPerRoot = 90 * time.Second
)
