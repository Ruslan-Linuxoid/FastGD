package mounts

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

func At(dir string) ([]string, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, fmt.Errorf("mounts: %w", err)
	}
	var types []string
	for line := range strings.SplitSeq(string(data), "\n") {

		fields := strings.Fields(line)
		if len(fields) < 7 || unescape(fields[4]) != dir {
			continue
		}
		if i := slices.Index(fields[6:len(fields)-1], "-"); i >= 0 {
			types = append(types, fields[6+i+1])
		}
	}
	return types, nil
}

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
