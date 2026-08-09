package executor

import (
	"bytes"
	"testing"
)

func TestTakeSSEEvents(t *testing.T) {
	tests := []struct {
		name   string
		buffer string
		events []string
		tail   string
	}{
		{
			name:   "no terminator yet",
			buffer: "event: message_start\ndata: {}\n",
			events: nil,
			tail:   "event: message_start\ndata: {}\n",
		},
		{
			name:   "one complete event",
			buffer: "event: ping\ndata: {}\n\n",
			events: []string{"event: ping\ndata: {}\n\n"},
		},
		{
			name:   "two events plus a partial tail",
			buffer: "event: a\ndata: 1\n\nevent: b\ndata: 2\n\nevent: c\n",
			events: []string{"event: a\ndata: 1\n\n", "event: b\ndata: 2\n\n"},
			tail:   "event: c\n",
		},
		{
			name: "crlf is normalised to lf",
			// The relay may frame with \r\n; the host's translation layer expects \n.
			buffer: "event: a\r\ndata: 1\r\n\r\n",
			events: []string{"event: a\ndata: 1\n\n"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events, tail := takeSSEEvents([]byte(test.buffer))
			if len(events) != len(test.events) {
				t.Fatalf("got %d events %q, want %d", len(events), events, len(test.events))
			}
			for i, want := range test.events {
				if string(events[i]) != want {
					t.Errorf("event %d = %q, want %q", i, events[i], want)
				}
			}
			if string(tail) != test.tail {
				t.Errorf("tail = %q, want %q", tail, test.tail)
			}
		})
	}
}

func TestSplitSSEEventsFlushesTail(t *testing.T) {
	body := []byte("event: a\ndata: 1\n\nevent: b\ndata: 2")

	if events := splitSSEEvents(body, false); len(events) != 1 {
		t.Errorf("without flush: got %d events, want 1", len(events))
	}
	events := splitSSEEvents(body, true)
	if len(events) != 2 {
		t.Fatalf("with flush: got %d events %q, want 2", len(events), events)
	}
	// A flushed partial still has to be a well-formed event or the host drops it.
	if want := "event: b\ndata: 2\n\n"; string(events[1]) != want {
		t.Errorf("flushed event = %q, want %q", events[1], want)
	}
}

func TestSplitSSEEventsIgnoresWhitespaceTail(t *testing.T) {
	// A body ending in its own terminator must not produce a bogus trailing event.
	if events := splitSSEEvents([]byte("event: a\ndata: 1\n\n\n"), true); len(events) != 1 {
		t.Errorf("got %d events %q, want 1", len(events), events)
	}
}

func TestNormalizeSSEEventTerminates(t *testing.T) {
	for _, in := range []string{"data: x", "data: x\n", "data: x\n\n"} {
		got := normalizeSSEEvent([]byte(in))
		if !bytes.HasSuffix(got, []byte("\n\n")) {
			t.Errorf("normalizeSSEEvent(%q) = %q, want it to end in a blank line", in, got)
		}
		if bytes.HasSuffix(got, []byte("\n\n\n")) {
			t.Errorf("normalizeSSEEvent(%q) = %q, want exactly one blank line", in, got)
		}
	}
}

// A stream arriving one byte at a time must frame exactly the same events as one that
// arrives in a single block - relayStream feeds whatever the HTTP bridge hands it.
func TestTakeSSEEventsIsChunkingInvariant(t *testing.T) {
	body := "event: a\ndata: 1\n\nevent: b\ndata: 22\n\nevent: c\ndata: 333\n\n"

	var whole []string
	for _, event := range splitSSEEvents([]byte(body), true) {
		whole = append(whole, string(event))
	}

	var buffer []byte
	var drip []string
	for i := 0; i < len(body); i++ {
		buffer = append(buffer, body[i])
		var events [][]byte
		events, buffer = takeSSEEvents(buffer)
		for _, event := range events {
			drip = append(drip, string(event))
		}
	}
	if len(buffer) != 0 {
		t.Errorf("leftover buffer %q, want empty", buffer)
	}
	if len(drip) != len(whole) {
		t.Fatalf("byte-at-a-time gave %d events, whole body gave %d", len(drip), len(whole))
	}
	for i := range whole {
		if drip[i] != whole[i] {
			t.Errorf("event %d: byte-at-a-time %q, whole %q", i, drip[i], whole[i])
		}
	}
}
