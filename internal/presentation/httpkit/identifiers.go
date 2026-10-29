package httpkit

import "regexp"

var safeID = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,128}$`)

// IsSafeID reports whether s is an identifier that may be stored, logged and echoed:
// 1-128 characters of A-Z a-z 0-9 . _ : @ -.
func IsSafeID(s string) bool {
	return safeID.MatchString(s)
}
