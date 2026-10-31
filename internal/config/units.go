package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseBytes parses a human byte size such as "10M", "1G", "512K", "1234",
// "1.5GiB", or "800MB". Single-letter and IEC (KiB) suffixes are binary (1024),
// matching how disk tools are usually read; two-letter SI suffixes (KB, MB, GB)
// are decimal (1000), matching docker and apt output. "0" is valid and an empty
// string is zero.
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	upper := strings.ToUpper(s)
	type unit struct {
		suffix string
		mult   int64
	}
	// Longest suffixes first so "GiB" is not read as "B" or "G".
	units := []unit{
		{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30}, {"TIB", 1 << 40}, {"PIB", 1 << 50}, {"EIB", 1 << 60},
		{"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12}, {"PB", 1e15}, {"EB", 1e18},
		{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"T", 1 << 40}, {"P", 1 << 50}, {"E", 1 << 60},
		{"B", 1},
	}
	for _, u := range units {
		if !strings.HasSuffix(upper, u.suffix) {
			continue
		}
		num := strings.TrimSpace(strings.TrimSuffix(upper, u.suffix))
		n, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size %q", s)
		}
		return int64(n * float64(u.mult)), nil
	}
	n, err := strconv.ParseInt(upper, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n, nil
}

// ParseDuration parses a duration, additionally accepting a "d" (day) suffix
// because config values such as max_age = "7d" are common.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}

// FormatBytes renders n as a compact human size.
func FormatBytes(n int64) string {
	if n < 0 {
		return "unknown"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < len("KMGT")+1; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
