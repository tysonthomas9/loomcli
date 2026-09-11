package fleet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// Native proof documents must not depend on duplicate-key resolution. Keep
// unknown numeric fields as JSON numbers/raw bytes rather than float64 values.
func validateRecoveryJSON(data []byte) error {
	if len(data) > recoveryBodyLimit || !utf8.Valid(data) {
		return fmt.Errorf("invalid recovery JSON encoding or size")
	}
	if err := validateRecoverySurrogates(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkRecoveryJSON(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing recovery JSON")
	}
	return nil
}
func walkRecoveryJSON(decoder *json.Decoder, depth int) error {
	if depth > 512 {
		return fmt.Errorf("recovery JSON nesting exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return walkRecoveryObject(decoder, depth)
	case '[':
		for decoder.More() {
			if err := walkRecoveryJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected recovery JSON delimiter")
	}
}
func walkRecoveryObject(decoder *json.Decoder, depth int) error {
	keys := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || keys[name] {
			return fmt.Errorf("duplicate or invalid recovery JSON key")
		}
		keys[name] = true
		if err := walkRecoveryJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

// encoding/json replaces unpaired surrogate escapes with U+FFFD. Reject them
// before decoding so keys and values retain the same identity in JavaScript.
func validateRecoverySurrogates(data []byte) error {
	inString := false
	for i := 0; i < len(data); {
		if data[i] == '"' {
			inString = !inString
			i++
			continue
		}
		if !inString || data[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(data) {
			return fmt.Errorf("incomplete recovery JSON escape")
		}
		if data[i+1] != 'u' {
			i += 2 // An escaped backslash does not begin a Unicode escape.
			continue
		}
		unit, ok := recoveryUnicodeEscape(data[i:])
		if !ok {
			return fmt.Errorf("invalid recovery JSON Unicode escape")
		}
		switch {
		case unit >= 0xDC00 && unit <= 0xDFFF:
			return fmt.Errorf("unpaired recovery JSON low surrogate")
		case unit >= 0xD800 && unit <= 0xDBFF:
			low, paired := recoveryUnicodeEscape(data[i+6:])
			if !paired || low < 0xDC00 || low > 0xDFFF {
				return fmt.Errorf("unpaired recovery JSON high surrogate")
			}
			i += 12
		default:
			i += 6
		}
	}
	return nil
}

func recoveryUnicodeEscape(data []byte) (uint16, bool) {
	if len(data) < 6 || data[0] != '\\' || data[1] != 'u' {
		return 0, false
	}
	var value uint16
	for _, digit := range data[2:6] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit - 'a' + 10)
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit - 'A' + 10)
		default:
			return 0, false
		}
	}
	return value, true
}
