package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"strings"
)

// NewSetupToken returns a one-time token such as "K7QM-2XRA-P4TD". It is
// printed to the log on first start so that only someone with access to the
// server can create the initial admin account.
func NewSetupToken() string {
	t := rand.Text()[:12]
	return t[0:4] + "-" + t[4:8] + "-" + t[8:12]
}

// SetupTokenMatches compares tokens, ignoring case, dashes and spaces. The
// token alphabet (base32) has no 0 or 1, so those are read as O and I.
func SetupTokenMatches(expected, given string) bool {
	normalise := func(s string) string {
		return strings.NewReplacer("-", "", " ", "", "0", "O", "1", "I").Replace(strings.ToUpper(s))
	}
	e, g := normalise(expected), normalise(given)
	return e != "" && subtle.ConstantTimeCompare([]byte(e), []byte(g)) == 1
}
