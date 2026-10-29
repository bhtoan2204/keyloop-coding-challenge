package presentation

import (
	"net/http"
	"net/url"
	"strconv"

	"keyloop-coding-challenge/internal/domain"
)

// queryParams rejects unknown or repeated parameters, so a typo (e.g. "agng=true")
// cannot silently return an unfiltered list.
func queryParams(r *http.Request, allowed ...string) (url.Values, error) {
	params := r.URL.Query()
	known := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		known[k] = true
	}
	for k, v := range params {
		if !known[k] {
			return nil, badRequest("INVALID_QUERY", "unknown query parameter "+strconv.Quote(k))
		}
		if len(v) > 1 {
			return nil, badRequest("INVALID_QUERY", "query parameter "+strconv.Quote(k)+" is repeated")
		}
	}
	return params, nil
}

func optionalInt(p url.Values, name string) (*int, error) {
	raw := p.Get(name)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil, invalidField(name, "must be an integer")
	}
	return &v, nil
}

func intOrZero(p url.Values, name string) (int, error) {
	v, err := optionalInt(p, name)
	if err != nil || v == nil {
		return 0, err
	}
	return *v, nil
}

func optionalBool(p url.Values, name string) (bool, error) {
	switch p.Get(name) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	}
	return false, invalidField(name, "must be true or false")
}

func optionalDate(p url.Values, name string) (*domain.Date, error) {
	raw := p.Get(name)
	if raw == "" {
		return nil, nil
	}
	d, err := domain.ParseDate(raw)
	if err != nil {
		return nil, invalidField(name, "must be a date in YYYY-MM-DD format")
	}
	return &d, nil
}
