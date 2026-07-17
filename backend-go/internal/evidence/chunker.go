package evidence

import (
	"fmt"
	"unicode/utf8"
)

type byteRange struct {
	StartByte int
	EndByte   int
	StartRune int
	EndRune   int
	Content   string
}

func splitCanonicalText(text string, maxBytes int, overlapBytes int) ([]byteRange, error) {
	if text == "" {
		return nil, nil
	}
	if maxBytes <= 0 || overlapBytes < 0 || overlapBytes >= maxBytes {
		return nil, fmt.Errorf("invalid chunk bounds max=%d overlap=%d", maxBytes, overlapBytes)
	}
	data := []byte(text)
	result := make([]byteRange, 0, len(data)/maxBytes+1)
	for start := 0; start < len(data); {
		end := len(data)
		if end-start > maxBytes {
			end = preferredEnd(data, start, start+maxBytes, maxBytes*3/5)
		}
		if end <= start || !utf8.Valid(data[start:end]) {
			return nil, fmt.Errorf("chunk [%d,%d) is not valid UTF-8", start, end)
		}
		result = append(result, byteRange{
			StartByte: start,
			EndByte:   end,
			StartRune: utf8.RuneCount(data[:start]),
			EndRune:   utf8.RuneCount(data[:end]),
			Content:   string(data[start:end]),
		})
		if end == len(data) {
			break
		}
		next := end - overlapBytes
		for next < end && next < len(data) && !utf8.RuneStart(data[next]) {
			next++
		}
		if next <= start {
			next = end
		}
		start = next
	}
	return result, nil
}

func preferredEnd(data []byte, start int, limit int, minimumLength int) int {
	if limit >= len(data) {
		return len(data)
	}
	for limit > start && !utf8.RuneStart(data[limit]) {
		limit--
	}
	minimum := start + minimumLength
	for minimum < limit && !utf8.RuneStart(data[minimum]) {
		minimum++
	}
	best := 0
	for position := minimum; position < limit; {
		r, size := utf8.DecodeRune(data[position:limit])
		if isPreferredBoundary(r) {
			best = position + size
		}
		position += size
	}
	if best > start {
		return best
	}
	return limit
}

func isPreferredBoundary(r rune) bool {
	switch r {
	case '\n', '.', '!', '?', ';', ':', ',', '。', '！', '？', '；', '：', '，':
		return true
	default:
		return false
	}
}
