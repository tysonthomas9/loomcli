package terminal

import (
	"fmt"
	"time"
)

// RecordingRun is a contiguous span of terminal cells with one style.
// Colors are serialized as CSS-compatible #rrggbb strings; an empty color
// means the terminal's default foreground/background.
type RecordingRun struct {
	Text      string `json:"text"`
	FG        string `json:"fg,omitempty"`
	BG        string `json:"bg,omitempty"`
	Bold      bool   `json:"bold,omitempty"`
	Italic    bool   `json:"italic,omitempty"`
	Underline bool   `json:"underline,omitempty"`
	Inverse   bool   `json:"inverse,omitempty"`
}

// RecordingLine is one immutable row in the durable line log.
type RecordingLine struct {
	Index     uint64         `json:"i"`
	Timestamp int64          `json:"t"`
	Offset    string         `json:"off,omitempty"`
	Cols      uint16         `json:"cols"`
	Runs      []RecordingRun `json:"runs"`
}

// RecordingResize records the live terminal geometry applied to subsequent
// output. Committed rows retain their own Cols and are never reflowed.
type RecordingResize struct {
	Timestamp int64  `json:"t"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

// RecordingUnhandledSequences is a bounded diagnostic summary of parsed
// escape sequences that the focused recording emulator intentionally did not
// implement. Count is exact; Prefixes is a bounded heavy-hitter map.
type RecordingUnhandledSequences struct {
	Count    uint64            `json:"count"`
	Prefixes map[string]uint64 `json:"prefixes"`
}

// RecordingMeta is the durable per-session summary stored in meta.json.
type RecordingMeta struct {
	FormatVersion        uint8                       `json:"formatVersion"`
	Generation           string                      `json:"generation"`
	SessionKey           string                      `json:"sessionKey"`
	IssueID              string                      `json:"issueId,omitempty"`
	StartedAt            int64                       `json:"startedAt"`
	Cols                 uint16                      `json:"cols"`
	Rows                 uint16                      `json:"rows"`
	Resizes              []RecordingResize           `json:"resizes"`
	LineCount            uint64                      `json:"lineCount"`
	RawLen               uint64                      `json:"rawLen"`
	AltScreen            bool                        `json:"altScreen"`
	Gaps                 uint64                      `json:"gaps"`
	PendingGap           bool                        `json:"pendingGap,omitempty"`
	UnhandledSequences   RecordingUnhandledSequences `json:"unhandledSequences"`
	HistoryLimited       bool                        `json:"historyLimited,omitempty"`
	RecordingStopped     bool                        `json:"recordingStopped,omitempty"`
	Closed               bool                        `json:"closed"`
	CheckpointGeneration uint64                      `json:"checkpointGeneration,omitempty"`
}

// RecordingPointer is the small Redis value used to discover a recording
// without putting terminal payload bytes in the in-process Redis database.
type RecordingPointer struct {
	Dir        string `json:"dir"`
	Generation string `json:"generation"`
	LineCount  uint64 `json:"lineCount"`
	RawLen     uint64 `json:"rawLen"`
	StartedAt  int64  `json:"startedAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// RecordingHistory is the bounded range returned by the history API.
type RecordingHistory struct {
	Generation      string          `json:"generation"`
	Lines           []RecordingLine `json:"lines"`
	TotalLines      uint64          `json:"totalLines"`
	FirstScreenLine uint64          `json:"firstScreenLine"`
	UpToDate        bool            `json:"upToDate"`
	Closed          bool            `json:"closed"`
	Cols            uint16          `json:"cols"`
	Immutable       bool            `json:"-"`
}

// OpaqueRecordingOffset formats a lexicographically sortable cursor within one
// recording generation. The opaque recording generation scopes the directory
// and API request, so the numeric prefix remains reserved at zero in format v1.
func OpaqueRecordingOffset(byteOffset uint64) string {
	return fmt.Sprintf("%016d_%016d", 0, byteOffset)
}

func unixMilliNow() int64 { return time.Now().UnixMilli() }
