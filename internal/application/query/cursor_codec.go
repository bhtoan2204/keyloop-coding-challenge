package query

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

func encode(v interface{}) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decode(s string, v interface{}) error {
	if len(s) > 512 {
		return fmt.Errorf("cursor too long")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
