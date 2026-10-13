// Package srt shifts SubRip timestamps without touching anything else in the file.
package srt

import (
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf8"
)

var ts = regexp.MustCompile(`(\d\d):(\d\d):(\d\d)[,.](\d{3}) --> (\d\d):(\d\d):(\d\d)[,.](\d{3})`)

func fmtTS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

func atoi(b []byte) int64 {
	n, _ := strconv.ParseInt(string(b), 10, 64)
	return n
}

// Shift moves every cue by ms milliseconds (negative = earlier). Times are
// clamped at zero. A UTF-8 byte-order mark is dropped. Returns the new text and
// the number of cues shifted.
func Shift(data []byte, ms int64) ([]byte, int) {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}
	n := 0
	out := ts.ReplaceAllFunc(data, func(m []byte) []byte {
		g := ts.FindSubmatch(m)
		start := atoi(g[1])*3600000 + atoi(g[2])*60000 + atoi(g[3])*1000 + atoi(g[4])
		end := atoi(g[5])*3600000 + atoi(g[6])*60000 + atoi(g[7])*1000 + atoi(g[8])
		n++
		return []byte(fmtTS(start+ms) + " --> " + fmtTS(end+ms))
	})
	return out, n
}

// CountCues returns how many timestamp lines a file has.
func CountCues(data []byte) int { return len(ts.FindAllIndex(data, -1)) }

// HasCyrillic reports whether the text contains Cyrillic letters.
func HasCyrillic(data []byte) bool {
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r >= 0x0400 && r <= 0x04FF {
			return true
		}
		data = data[size:]
	}
	return false
}
