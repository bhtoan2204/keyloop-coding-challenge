package httpkit

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"keyloop-coding-challenge/internal/domain"
)

// maxBodyBytes limits command request bodies; the largest valid body is a few KB.
const maxBodyBytes = 64 << 10

// DecodeJSON strictly decodes a single JSON object: application/json only, bounded
// size, unknown fields rejected, no trailing data. An empty body is allowed only when
// allowEmpty is set.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}, allowEmpty bool) error {
	if r.ContentLength == 0 && allowEmpty && r.Header.Get("Content-Type") == "" {
		return nil
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return &Error{
			status: http.StatusUnsupportedMediaType,
			code:   "UNSUPPORTED_MEDIA_TYPE",
			detail: "Content-Type must be application/json",
		}
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &tooLarge):
			return &Error{
				status: http.StatusRequestEntityTooLarge,
				code:   "BODY_TOO_LARGE",
				detail: "request body exceeds 64 KiB",
			}
		case errors.Is(err, io.EOF):
			if allowEmpty {
				return nil
			}
			return BadRequest("INVALID_BODY", "request body is required")
		case errors.As(err, &typeErr):
			field := typeErr.Field
			if field == "" {
				field = "body" // the top-level value is not a JSON object
			}
			return InvalidField(field, "has the wrong type")
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			return BadRequest("INVALID_BODY", strings.TrimPrefix(err.Error(), "json: "))
		default:
			return BadRequest("INVALID_BODY", "request body is not valid JSON")
		}
	}
	if dec.More() {
		return BadRequest("INVALID_BODY", "request body must contain a single JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return BadRequest("INVALID_BODY", "request body must contain a single JSON object")
	}
	return nil
}

func ParseDate(field, value string) (domain.Date, error) {
	if value == "" {
		return domain.Date{}, InvalidField(field, "is required")
	}
	d, err := domain.ParseDate(value)
	if err != nil {
		return domain.Date{}, InvalidField(field, "must be a date in YYYY-MM-DD format")
	}
	return d, nil
}
