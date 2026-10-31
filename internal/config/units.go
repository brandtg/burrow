package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseBytes parses a human byte size such as "10M", "1G", "512K", or "1234".
// Both SI (1000) and binary (1024) suffixes are accepted; the single-letter
// suffix is binary, matching how disk tools are usually read. "0" is valid.
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	upper := strings.ToUpper(s)
	var mult int64
	switch {
	case strings.HasSuffix(upper, "K"), strings.HasSuffix(upper, "KIB"):
		mult = 1 << 10
	case strings.HasSuffix(upper, "M"), strings.HasSuffix(upper, "MIB"):
		mult = 1 << 20
	case strings.HasSuffix(upper, "G"), strings.HasSuffix(upper, "GIB"):
		mult = 1 << 30
	case strings.HasSuffix(upper, "T"), strings.HasSuffix(upper, "TIB"):
		mult = 1 << 40
	case strings.HasSuffix(upper, "B"):
		mult = 1
	default:
		n, err := strconv.ParseInt(upper, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid size %q", s)
		}
		return n, nil
	}
	num := upper
	for _, suffix := range []string{"KIB", "MIB", "GIB", "TIB", "B", "K", "M", "G", "T"} {
		if strings.HasSuffix(num, suffix) {
			num = strings.TrimSuffix(num, suffix)
			break
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return int64(n * float64(mult)), nil
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
