package executor

import "bytes"

// takeSSEEvents splits off every complete event, returning the unconsumed tail.
//
// An event ends at a blank line. Lines are normalised to \n and each emitted event keeps
// its terminating blank line, matching what the native Claude executor produces.
func takeSSEEvents(buffer []byte) ([][]byte, []byte) {
	events := make([][]byte, 0, 4)
	for {
		end := indexAfterBlankLine(buffer)
		if end < 0 {
			return events, buffer
		}
		events = append(events, normalizeSSEEvent(buffer[:end]))
		buffer = bytes.Clone(buffer[end:])
	}
}

// splitSSEEvents frames a complete body. When flush is set, a trailing partial event is
// emitted as well rather than dropped.
func splitSSEEvents(body []byte, flush bool) [][]byte {
	events, tail := takeSSEEvents(body)
	if flush && len(bytes.TrimSpace(tail)) > 0 {
		events = append(events, normalizeSSEEvent(tail))
	}
	return events
}

// indexAfterBlankLine finds the first event terminator, returning the offset just past
// it, or -1 when the buffer holds no complete event yet.
func indexAfterBlankLine(buffer []byte) int {
	for offset := 0; offset < len(buffer); offset++ {
		if buffer[offset] != '\n' {
			continue
		}
		rest := buffer[offset+1:]
		switch {
		case len(rest) >= 2 && rest[0] == '\r' && rest[1] == '\n':
			return offset + 3
		case len(rest) >= 1 && rest[0] == '\n':
			return offset + 2
		}
	}
	return -1
}

func normalizeSSEEvent(event []byte) []byte {
	normalized := bytes.ReplaceAll(event, []byte("\r\n"), []byte("\n"))
	if !bytes.HasSuffix(normalized, []byte("\n\n")) {
		if !bytes.HasSuffix(normalized, []byte("\n")) {
			normalized = append(normalized, '\n')
		}
		normalized = append(normalized, '\n')
	}
	return normalized
}
