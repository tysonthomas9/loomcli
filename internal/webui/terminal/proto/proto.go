// Package proto implements the websocket-independent loom-terminal.v1 codec.
package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	HeaderSize        = 28
	Magic      uint16 = 0x4c54
	Version    uint8  = 1

	KindInitialState  uint8 = 0x01
	KindOutput        uint8 = 0x02
	KindResize        uint8 = 0x03
	KindNotice        uint8 = 0x04
	KindClose         uint8 = 0x05
	KindInput         uint8 = 0x81
	KindResizeRequest uint8 = 0x82
	KindFocus         uint8 = 0x83
)

var (
	ErrShortFrame       = errors.New("terminal frame too short")
	ErrBadMagic         = errors.New("terminal frame has bad magic")
	ErrBadVersion       = errors.New("terminal frame has unsupported version")
	ErrUnknownKind      = errors.New("terminal frame has unknown kind")
	ErrMalformedPayload = errors.New("terminal frame has malformed payload")
)

// Frame is the decoded representation of either a server or client frame.
// Data is used by output, input, close, and initial_state; Code and Message
// are used by notice.
type Frame struct {
	Kind          uint8
	Generation    [16]byte
	Sequence      uint64
	Cols, Rows    uint16
	RetainedLines uint32
	Encoding      string
	Data          []byte
	Reason        string
	Code, Message string
}

type noticePayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Encode(frame Frame) ([]byte, error) {
	payload, err := encodePayload(frame)
	if err != nil {
		return nil, err
	}
	out := make([]byte, HeaderSize+len(payload))
	binary.BigEndian.PutUint16(out[0:2], Magic)
	out[2] = Version
	out[3] = frame.Kind
	copy(out[4:20], frame.Generation[:])
	binary.BigEndian.PutUint64(out[20:28], frame.Sequence)
	copy(out[HeaderSize:], payload)
	return out, nil
}

func encodePayload(frame Frame) ([]byte, error) {
	switch frame.Kind {
	case KindInitialState:
		if len(frame.Encoding) > 255 || !utf8.ValidString(frame.Encoding) {
			return nil, fmt.Errorf("invalid encoding")
		}
		p := make([]byte, 9+len(frame.Encoding)+len(frame.Data))
		binary.BigEndian.PutUint16(p[0:2], frame.Cols)
		binary.BigEndian.PutUint16(p[2:4], frame.Rows)
		binary.BigEndian.PutUint32(p[4:8], frame.RetainedLines)
		p[8] = byte(len(frame.Encoding)) //nolint:gosec // encoding length is bounded to uint8 above
		copy(p[9:], frame.Encoding)
		copy(p[9+len(frame.Encoding):], frame.Data)
		return p, nil
	case KindOutput, KindInput:
		return append([]byte(nil), frame.Data...), nil
	case KindResize, KindResizeRequest:
		p := make([]byte, 4)
		binary.BigEndian.PutUint16(p[0:2], frame.Cols)
		binary.BigEndian.PutUint16(p[2:4], frame.Rows)
		return p, nil
	case KindNotice:
		if !utf8.ValidString(frame.Code) || !utf8.ValidString(frame.Message) {
			return nil, fmt.Errorf("invalid notice text")
		}
		return json.Marshal(noticePayload{Code: frame.Code, Message: frame.Message})
	case KindClose:
		if !utf8.ValidString(frame.Reason) {
			return nil, fmt.Errorf("invalid close reason")
		}
		return []byte(frame.Reason), nil
	case KindFocus:
		return nil, nil
	default:
		return nil, ErrUnknownKind
	}
}

func Decode(data []byte) (Frame, error) {
	if len(data) < HeaderSize {
		return Frame{}, ErrShortFrame
	}
	if binary.BigEndian.Uint16(data[:2]) != Magic {
		return Frame{}, ErrBadMagic
	}
	if data[2] != Version {
		return Frame{}, ErrBadVersion
	}
	kind := data[3]
	if !knownKind(kind) {
		return Frame{}, ErrUnknownKind
	}
	f := Frame{Kind: kind, Sequence: binary.BigEndian.Uint64(data[20:28])}
	copy(f.Generation[:], data[4:20])
	return decodePayload(f, data[HeaderSize:])
}

func decodePayload(f Frame, p []byte) (Frame, error) {
	kind := f.Kind
	switch kind {
	case KindInitialState:
		if len(p) < 9 || len(p) < 9+int(p[8]) {
			return Frame{}, ErrMalformedPayload
		}
		f.Cols = binary.BigEndian.Uint16(p[0:2])
		f.Rows = binary.BigEndian.Uint16(p[2:4])
		f.RetainedLines = binary.BigEndian.Uint32(p[4:8])
		encodingEnd := 9 + int(p[8])
		if !utf8.Valid(p[9:encodingEnd]) {
			return Frame{}, ErrMalformedPayload
		}
		f.Encoding = string(p[9:encodingEnd])
		f.Data = append([]byte(nil), p[encodingEnd:]...)
	case KindOutput, KindInput:
		f.Data = append([]byte(nil), p...)
	case KindResize, KindResizeRequest:
		if len(p) != 4 {
			return Frame{}, ErrMalformedPayload
		}
		f.Cols = binary.BigEndian.Uint16(p[:2])
		f.Rows = binary.BigEndian.Uint16(p[2:])
	case KindNotice:
		if !utf8.Valid(p) {
			return Frame{}, ErrMalformedPayload
		}
		var n noticePayload
		if err := json.Unmarshal(p, &n); err != nil {
			return Frame{}, fmt.Errorf("%w: notice: %v", ErrMalformedPayload, err)
		}
		f.Code, f.Message = n.Code, n.Message
	case KindClose:
		if !utf8.Valid(p) {
			return Frame{}, ErrMalformedPayload
		}
		f.Reason = string(p)
	case KindFocus:
		if len(p) != 0 {
			return Frame{}, ErrMalformedPayload
		}
	}
	return f, nil
}

func knownKind(kind uint8) bool {
	switch kind {
	case KindInitialState, KindOutput, KindResize, KindNotice, KindClose, KindInput, KindResizeRequest, KindFocus:
		return true
	default:
		return false
	}
}
