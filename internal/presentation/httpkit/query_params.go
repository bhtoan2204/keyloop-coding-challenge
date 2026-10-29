package httpkit

import (
	"net/http"
	"net/url"
	"strconv"

	"keyloop-coding-challenge/internal/domain"
)

// QueryParams rejects unknown or repeated parameters, so a typo (e.g. "agng=true")
// cannot silently return an unfiltered list.
func QueryParams(r *http.Request, allowed ...string) (url.Values, error) {
	params := r.URL.Query()
	known := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		known[k] = true
	}
	for k, v := range params {
		if !known[k] {
			return nil, BadRequest("INVALID_QUERY", "unknown query parameter "+strconv.Quote(k))
		}
		if len(v) > 1 {
			return nil, BadRequest("INVALID_QUERY", "query parameter "+strconv.Quote(k)+" is repeated")
		}
	}
	return params, nil
}

func OptionalInt(p url.Values, name string) (*int, error) {
	raw := p.Get(name)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil, InvalidField(name, "must be an integer")
	}
	return &v, nil
}

// IntOrZero is OptionalInt with 0 for an absent parameter (the service applies its default).
func IntOrZero(p url.Values, name string) (int, error) {
	v, err := OptionalInt(p, name)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

// OptionalBool accepts only "true" and "false"; absent means false.
func OptionalBool(p url.Values, name string) (bool, error) {
	switch p.Get(name) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	}
	return false, InvalidField(name, "must be true or false")
}

func OptionalDate(p url.Values, name string) (*domain.Date, error) {
	raw := p.Get(name)
	if raw == "" {
		return nil, nil
	}
	d, err := domain.ParseDate(raw)
	if err != nil {
		return nil, InvalidField(name, "must be a date in YYYY-MM-DD format")
	}
	return &d, nil
}
