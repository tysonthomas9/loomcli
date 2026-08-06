package sessioncoord

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const validCanonicalTranscriptEvent = `{"seq":1,"timestamp":"2026-07-28T12:00:00Z","role":"assistant","type":"text","text":"done"}`

func TestParseCanonicalTranscriptBytesDoesNotPreallocateFromBlankLines(t *testing.T) {
	events, err := parseCanonicalTranscriptBytes(bytes.Repeat([]byte("\n"), 1<<20))
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestParseCanonicalTranscriptBytesEnforcesLimitsWhileStreaming(t *testing.T) {
	t.Run("raw bytes", func(t *testing.T) {
		_, err := parseCanonicalTranscriptBytesWithinLimits([]byte("{}\n"), 2, 10)
		require.EqualError(t, err, "transcript exceeds 2-byte limit")
	})
	t.Run("NDJSON events", func(t *testing.T) {
		_, err := parseCanonicalTranscriptBytesWithinLimits(
			[]byte(strings.Repeat(validCanonicalTranscriptEvent+"\n", 3)),
			1_024,
			2,
		)
		require.EqualError(t, err, "transcript exceeds 2-event limit")
	})
	t.Run("array events", func(t *testing.T) {
		_, err := parseCanonicalTranscriptBytesWithinLimits(
			[]byte("["+strings.Join([]string{
				validCanonicalTranscriptEvent,
				validCanonicalTranscriptEvent,
				validCanonicalTranscriptEvent,
			}, ",")+"]"),
			1_024,
			2,
		)
		require.EqualError(t, err, "transcript exceeds 2-event limit")
	})
}

func TestParseCanonicalTranscriptBytesRejectsTrailingArrayJSON(t *testing.T) {
	_, err := parseCanonicalTranscriptBytes([]byte("[]{}"))
	require.ErrorContains(t, err, "trailing JSON")
}

func TestParseCanonicalTranscriptBytesAcceptsNDJSONWhitespace(t *testing.T) {
	events, err := parseCanonicalTranscriptBytes([]byte(
		strings.Repeat(" \n", 16) +
			validCanonicalTranscriptEvent +
			"\n",
	))
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "done", events[0].Text)
}

func TestParseCanonicalTranscriptBytesRejectsInvalidCanonicalEvents(t *testing.T) {
	t.Run("unknown role in stream", func(t *testing.T) {
		_, err := parseCanonicalTranscriptBytes([]byte(
			`{"seq":1,"timestamp":"2026-07-28T12:00:00Z","role":"operator","type":"text","text":"done"}` + "\n",
		))
		require.EqualError(t, err, `transcript event has unknown role "operator"`)
	})

	t.Run("unknown type in array", func(t *testing.T) {
		_, err := parseCanonicalTranscriptBytes([]byte(
			`[{"seq":1,"timestamp":"2026-07-28T12:00:00Z","role":"assistant","type":"thought","text":"done"}]`,
		))
		require.EqualError(t, err, `transcript event has unknown type "thought"`)
	})

	t.Run("zero timestamp in stream", func(t *testing.T) {
		_, err := parseCanonicalTranscriptBytes([]byte(
			`{"seq":1,"role":"assistant","type":"text","text":"done"}` + "\n",
		))
		require.EqualError(t, err, "transcript event timestamp is required")
	})
}
