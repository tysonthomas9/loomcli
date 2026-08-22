package proto

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

var testGeneration = [16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

func TestVectorsDecodeAndReencode(t *testing.T) {
	tests := []struct {
		name string
		hex  string
		want Frame
	}{
		{"initial_state", "4c540101000102030405060708090a0b0c0d0e0f0102030405060708" +
			"005000180000002a0a787465726d2d76742f311b5b33316d6869", Frame{Kind: KindInitialState, Generation: testGeneration, Sequence: 0x0102030405060708, Cols: 80, Rows: 24, RetainedLines: 42, Encoding: "xterm-vt/1", Data: []byte("\x1b[31mhi")}},
		{"output", "4c540102000102030405060708090a0b0c0d0e0f000000000000000968690a", Frame{Kind: KindOutput, Generation: testGeneration, Sequence: 9, Data: []byte("hi\n")}},
		{"resize", "4c540103000102030405060708090a0b0c0d0e0f000000000000000a00780028", Frame{Kind: KindResize, Generation: testGeneration, Sequence: 10, Cols: 120, Rows: 40}},
		{"notice", "4c540104000102030405060708090a0b0c0d0e0f000000000000000b7b22636f6465223a22696e7075745f64726f70706564222c226d657373616765223a22496e7075742064726f70706564227d", Frame{Kind: KindNotice, Generation: testGeneration, Sequence: 11, Code: "input_dropped", Message: "Input dropped"}},
		{"notice_conn_id", "4c540104000102030405060708090a0b0c0d0e0f000000000000000b7b22636f6465223a22696e7075745f64726f70706564222c226d657373616765223a22496e7075742064726f70706564222c22636f6e6e5f6964223a22636f6e6e2d31e28094227d", Frame{Kind: KindNotice, Generation: testGeneration, Sequence: 11, Code: "input_dropped", Message: "Input dropped", ConnID: "conn-1—"}},
		{"close", "4c540105000102030405060708090a0b0c0d0e0f000000000000000c657869746564", Frame{Kind: KindClose, Generation: testGeneration, Sequence: 12, Reason: "exited"}},
		{"input", "4c540181000102030405060708090a0b0c0d0e0f00000000000000006c730a", Frame{Kind: KindInput, Generation: testGeneration, Data: []byte("ls\n")}},
		{"resize_request", "4c540182000102030405060708090a0b0c0d0e0f000000000000000000780028", Frame{Kind: KindResizeRequest, Generation: testGeneration, Cols: 120, Rows: 40}},
		{"focus", "4c540183000102030405060708090a0b0c0d0e0f0000000000000000", Frame{Kind: KindFocus, Generation: testGeneration}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire, err := hex.DecodeString(tt.hex)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Decode(wire)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !framesEqual(got, tt.want) {
				t.Fatalf("frame = %#v, want %#v", got, tt.want)
			}
			encoded, err := Encode(got)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if !bytes.Equal(encoded, wire) {
				t.Fatalf("re-encoded %x, want %x", encoded, wire)
			}
		})
	}
	tsNames := map[string]string{
		"initial_state":  "initialState",
		"output":         "output",
		"resize":         "resize",
		"notice":         "notice",
		"notice_conn_id": "noticeWithConnID",
		"close":          "close",
		"input":          "input",
		"resize_request": "resizeRequest",
		"focus":          "focus",
	}
	tsVectors, err := os.ReadFile("testdata/vectors.ts.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		key := tsNames[tt.name]
		if got := extractTSHexVector(t, string(tsVectors), key); got != tt.hex {
			t.Errorf("TS vector %s = %q, want inline %q", key, got, tt.hex)
		}
	}
}

func extractTSHexVector(t *testing.T, source, key string) string {
	t.Helper()
	re := regexp.MustCompile(`(?ms)^\s*` + regexp.QuoteMeta(key) + `:\s*((?:"[0-9a-fA-F]+"\s*(?:\+\s*)?)+)`)
	matches := re.FindStringSubmatch(source)
	if len(matches) != 2 {
		t.Fatalf("TS vector %q not found", key)
	}
	parts := regexp.MustCompile(`"([0-9a-fA-F]+)"`).FindAllStringSubmatch(matches[1], -1)
	return strings.Join(func() []string {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			out = append(out, part[1])
		}
		return out
	}(), "")
}

func TestMalformedFrames(t *testing.T) {
	base := make([]byte, HeaderSize)
	base[0], base[1], base[2], base[3] = 0x4c, 0x54, Version, KindInitialState
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"short", base[:HeaderSize-1], ErrShortFrame},
		{"bad magic", append([]byte{0, 0}, base[2:]...), ErrBadMagic},
		{"bad version", func() []byte { b := append([]byte(nil), base...); b[2] = 2; return b }(), ErrBadVersion},
		{"unknown kind", func() []byte { b := append([]byte(nil), base...); b[3] = 0x7f; return b }(), ErrUnknownKind},
		{"truncated initial state", append(base, 0, 1, 0, 2, 0, 0, 0, 0, 5), ErrMalformedPayload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.data)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func framesEqual(a, b Frame) bool {
	return a.Kind == b.Kind && a.Generation == b.Generation && a.Sequence == b.Sequence &&
		a.Cols == b.Cols && a.Rows == b.Rows && a.RetainedLines == b.RetainedLines &&
		a.Encoding == b.Encoding && bytes.Equal(a.Data, b.Data) && a.Reason == b.Reason &&
		a.Code == b.Code && a.Message == b.Message
}
