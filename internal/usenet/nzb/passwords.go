package nzb

import (
	"regexp"
	"slices"
	"strings"
)

var (
	// "Passwort: geheim", "pw=geheim", "[PW: geheim]", "password - geheim"
	passwordPattern = regexp.MustCompile(`(?i)\b(?:passwor[dt]|kennwort|pass|pw)\b\s*[:=\-]\s*["']?([^\s"'\])}<>|]{2,64})`)
	urlPattern      = regexp.MustCompile(`(?i)(https?://|www\.)\S+|\b[a-z0-9-]+\.(com|net|org|info|biz|xyz|to|cc)\b`)
	passwordWord    = regexp.MustCompile(`passwor[dt]|kennwort|\bpw\b`)
)

// PasswordsFromText finds archive passwords in free text such as NFO files
// or posting subjects.
func PasswordsFromText(text string) []string {
	var out []string
	for _, m := range filenamePassword.FindAllStringSubmatch(text, -1) {
		out = appendUnique(out, m[1])
	}
	for _, m := range passwordPattern.FindAllStringSubmatch(text, -1) {
		pw := strings.TrimRight(m[1], ".,;")
		if urlPattern.MatchString(pw) {
			continue
		}
		out = appendUnique(out, pw)
	}
	return out
}

// PasswordsFromSubjects collects passwords mentioned in the subjects.
func (n *NZB) PasswordsFromSubjects() []string {
	var out []string
	for _, f := range n.Files {
		for _, pw := range PasswordsFromText(f.Subject) {
			out = appendUnique(out, pw)
		}
	}
	return out
}

// LooksLikeSpam reports texts that send the reader to a web site for the
// password ("Passwort auf www.example.com") – a common trap of fake posts.
func LooksLikeSpam(text string) bool {
	lower := strings.ToLower(text)
	for _, loc := range passwordWord.FindAllStringIndex(lower, -1) {
		window := lower[max(loc[0]-120, 0):min(loc[1]+120, len(lower))]
		if urlPattern.MatchString(window) {
			return true
		}
	}
	return false
}

func appendUnique(list []string, s string) []string {
	if s == "" || slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}
