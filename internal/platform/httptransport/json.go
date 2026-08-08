// Package httptransport owns capability-neutral HTTP wire mechanics shared by
// inbound adapters. It contains no capability policy or response mapping.
package httptransport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// DefaultMaxJSONBodyBytes is the default request-body ceiling (1 MiB).
const DefaultMaxJSONBodyBytes = 1 << 20

// ErrTrailingJSON reports that a body contains content after its first JSON
// value. Adapters retain ownership of the public status, code, and message.
var ErrTrailingJSON = errors.New("request body must contain exactly one JSON value")

// JSONDecodeOptions defines the transport policy for one JSON body.
type JSONDecodeOptions struct {
	MaxBytes              int64
	DisallowUnknownFields bool
}

// DecodeOneJSONRequest bounds and decodes exactly one JSON request value.
func DecodeOneJSONRequest(w http.ResponseWriter, r *http.Request, dst any, options JSONDecodeOptions) error {
	limit := decodeLimit(options)
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return decodeOneJSON(json.NewDecoder(r.Body), dst, options)
}

// DecodeOneJSONBytes applies the same policy to a body that an adapter had to
// materialize before dispatch, while retaining a defensive size check.
func DecodeOneJSONBytes(data []byte, dst any, options JSONDecodeOptions) error {
	limit := decodeLimit(options)
	if int64(len(data)) > limit {
		return &http.MaxBytesError{Limit: limit}
	}
	return decodeOneJSON(json.NewDecoder(bytes.NewReader(data)), dst, options)
}

func decodeLimit(options JSONDecodeOptions) int64 {
	if options.MaxBytes > 0 {
		return options.MaxBytes
	}
	return DefaultMaxJSONBodyBytes
}

func decodeOneJSON(decoder *json.Decoder, dst any, options JSONDecodeOptions) error {
	if options.DisallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		// Preserve both facts for adapter-local projection: this is trailing
		// content, and it may also be an over-limit or syntax failure.
		return errors.Join(ErrTrailingJSON, err)
	}
	return ErrTrailingJSON
}
