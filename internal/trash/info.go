package trash

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"time"
)

const (
	infoHeader       = "[Trash Info]"
	infoKeyPath      = "Path"
	infoKeyDate      = "DeletionDate"
	deletionDateForm = "2006-01-02T15:04:05"
)

// Info is the parsed contents of a .trashinfo file.
type Info struct {
	Path         string
	DeletionDate time.Time
}

// encodeInfo renders a .trashinfo file. Path is stored percent-encoded with
// '/' left intact, per the freedesktop Trash Specification.
func encodeInfo(origPath string, when time.Time) []byte {
	var b bytes.Buffer
	b.WriteString(infoHeader)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "%s=%s\n", infoKeyPath, percentEncode(origPath))
	fmt.Fprintf(&b, "%s=%s\n", infoKeyDate, when.Format(deletionDateForm))
	return b.Bytes()
}

// parseInfo reads a .trashinfo file.
func parseInfo(data []byte) (Info, error) {
	var info Info
	sc := bufio.NewScanner(bytes.NewReader(data))
	first := true
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if first {
			first = false
			if strings.TrimSpace(line) != infoHeader {
				return Info{}, fmt.Errorf("trash: missing %q header", infoHeader)
			}
			continue
		}
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case infoKeyPath:
			decoded, err := percentDecode(value)
			if err != nil {
				return Info{}, fmt.Errorf("trash: bad Path value: %w", err)
			}
			info.Path = decoded
		case infoKeyDate:
			t, err := time.ParseInLocation(deletionDateForm, value, time.Local)
			if err != nil {
				return Info{}, fmt.Errorf("trash: bad DeletionDate: %w", err)
			}
			info.DeletionDate = t
		}
	}
	if err := sc.Err(); err != nil {
		return Info{}, err
	}
	if info.Path == "" {
		return Info{}, fmt.Errorf("trash: missing Path")
	}
	return info, nil
}

// percentEncode percent-encodes every byte that is not an RFC 3986 unreserved
// character or '/'. The freedesktop spec keeps the path separators readable.
func percentEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) || c == '/' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// percentDecode reverses percentEncode.
func percentDecode(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", fmt.Errorf("truncated escape")
		}
		hi := unhex(s[i+1])
		lo := unhex(s[i+2])
		if hi < 0 || lo < 0 {
			return "", fmt.Errorf("invalid escape %q", s[i:i+3])
		}
		b.WriteByte(byte(hi<<4 | lo))
		i += 2
	}
	return b.String(), nil
}

func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '-' || c == '.' || c == '_' || c == '~':
		return true
	}
	return false
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
