package release

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Password policies of a profile.
const (
	PasswordAllow  = "allow"  // download passworded releases
	PasswordKnown  = "known"  // only if the password is known up front
	PasswordAvoid  = "avoid"  // allowed, but ranked lower
	PasswordReject = "reject" // never
)

// WordScore adds Score to releases containing Term.
type WordScore struct {
	Term  string `json:"term"`
	Score int    `json:"score"`
}

// Profile describes which releases are acceptable and which are preferred.
// Terms match whole words case-insensitively ("German" matches
// "Movie.German.DL" but not "Germany"); "/regex/" uses a regular expression.
type Profile struct {
	// MaxResolution is the highest acceptable resolution in lines (0: any).
	MaxResolution int      `json:"maxResolution"`
	MinResolution int      `json:"minResolution"`
	RequiredWords []string `json:"requiredWords"`
	// RequiredMode is "any" (one required word suffices) or "all".
	RequiredMode   string      `json:"requiredMode"`
	ForbiddenWords []string    `json:"forbiddenWords"`
	PreferredWords []WordScore `json:"preferredWords"`
	// AllowLowQuality accepts cam, telesync, telecine and screener copies.
	AllowLowQuality bool `json:"allowLowQuality"`
	// Size limits in MB per minute of runtime (0: no limit).
	MinMBPerMinute float64 `json:"minMbPerMinute"`
	MaxMBPerMinute float64 `json:"maxMbPerMinute"`
	PreferredCodec string  `json:"preferredCodec"` // "", "h264", "h265", "av1"
	PasswordPolicy string  `json:"passwordPolicy"`
}

// DefaultProfile is used until the user configures one.
func DefaultProfile() Profile {
	return Profile{
		MaxResolution:  1080,
		RequiredMode:   "any",
		RequiredWords:  []string{},
		ForbiddenWords: []string{},
		PreferredWords: []WordScore{},
		MinMBPerMinute: 5,
		MaxMBPerMinute: 150,
		PasswordPolicy: PasswordAllow,
	}
}

// Validate checks the profile, e.g. that regular expressions compile.
func (p Profile) Validate() error {
	for _, t := range slices.Concat(p.RequiredWords, p.ForbiddenWords, termsOf(p.PreferredWords)) {
		if _, err := compileTerm(t); err != nil {
			return fmt.Errorf("invalid term %q: %w", t, err)
		}
	}
	switch p.RequiredMode {
	case "", "any", "all":
	default:
		return fmt.Errorf("invalid required mode %q", p.RequiredMode)
	}
	switch p.PasswordPolicy {
	case "", PasswordAllow, PasswordKnown, PasswordAvoid, PasswordReject:
	default:
		return fmt.Errorf("invalid password policy %q", p.PasswordPolicy)
	}
	if p.MaxResolution > 0 && p.MinResolution > p.MaxResolution {
		return fmt.Errorf("minimum resolution %dp is above the maximum %dp", p.MinResolution, p.MaxResolution)
	}
	return nil
}

func termsOf(ws []WordScore) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Term
	}
	return out
}

// Candidate is a release found by a search.
type Candidate struct {
	Title      string
	Size       int64
	Grabs      int
	Passworded bool
	// PasswordKnown is set when the NZB or title carries the password.
	PasswordKnown bool
}

// Rejection codes; the web UI translates them.
const (
	RejectResolutionHigh = "resolution_too_high"
	RejectResolutionLow  = "resolution_too_low"
	RejectRequired       = "required_word_missing"
	RejectForbidden      = "forbidden_word"
	RejectLowQuality     = "low_quality_source"
	RejectTooSmall       = "too_small"
	RejectTooLarge       = "too_large"
	RejectPassword       = "passworded"
	RejectBlocklisted    = "blocklisted"
	RejectWrongMovie     = "wrong_movie"
)

// Decision is the verdict on one candidate.
type Decision struct {
	Info  Info `json:"info"`
	Score int  `json:"score"`
	// Rejected is "code" or "code: detail"; empty when acceptable.
	Rejected string `json:"rejected,omitempty"`
}

// Evaluate checks c against the profile. runtime is the movie length in
// minutes (0 if unknown, which disables the size check).
func (p Profile) Evaluate(c Candidate, runtime int) Decision {
	info := Parse(c.Title)
	d := Decision{Info: info}
	reject := func(code, format string, args ...any) Decision {
		d.Rejected = code
		if format != "" {
			d.Rejected += ": " + fmt.Sprintf(format, args...)
		}
		return d
	}

	res := info.Resolution
	if res == 0 {
		res = 480 // untagged releases are usually SD
	}
	if p.MaxResolution > 0 && res > p.MaxResolution {
		return reject(RejectResolutionHigh, "%dp", res)
	}
	if p.MinResolution > 0 && res < p.MinResolution {
		return reject(RejectResolutionLow, "%dp", res)
	}
	if info.Source.LowQuality() && !p.AllowLowQuality {
		return reject(RejectLowQuality, "%s", info.Source)
	}
	for _, w := range p.ForbiddenWords {
		if matchTerm(w, c.Title) {
			return reject(RejectForbidden, "%s", w)
		}
	}
	if len(p.RequiredWords) > 0 {
		matched := 0
		for _, w := range p.RequiredWords {
			if matchTerm(w, c.Title) {
				matched++
			}
		}
		if matched == 0 || (p.RequiredMode == "all" && matched < len(p.RequiredWords)) {
			return reject(RejectRequired, "%s", strings.Join(p.RequiredWords, ", "))
		}
	}
	if runtime > 0 && c.Size > 0 {
		mbPerMin := float64(c.Size) / (1 << 20) / float64(runtime)
		if p.MinMBPerMinute > 0 && mbPerMin < p.MinMBPerMinute {
			return reject(RejectTooSmall, "%.1f MB/min", mbPerMin)
		}
		if p.MaxMBPerMinute > 0 && mbPerMin > p.MaxMBPerMinute {
			return reject(RejectTooLarge, "%.1f MB/min", mbPerMin)
		}
	}
	if c.Passworded && !c.PasswordKnown {
		switch p.PasswordPolicy {
		case PasswordReject, PasswordKnown:
			return reject(RejectPassword, "")
		case PasswordAvoid:
			d.Score -= 300
		}
	}

	d.Score += resolutionRank(res) * 1000
	d.Score += sourceScore[info.Source]
	for _, w := range p.PreferredWords {
		if matchTerm(w.Term, c.Title) {
			d.Score += w.Score
		}
	}
	if p.PreferredCodec != "" && info.Codec == p.PreferredCodec {
		d.Score += 50
	}
	if info.Proper {
		d.Score += 20
	}
	d.Score += min(c.Grabs, 100) / 10
	return d
}

var sourceScore = map[Source]int{
	SourceRemux:  500,
	SourceBluRay: 500,
	SourceWebDL:  450,
	SourceWebRip: 400,
	SourceHDTV:   300,
	SourceDVD:    200,
	"":           100,
}

func resolutionRank(lines int) int {
	switch {
	case lines >= 2160:
		return 5
	case lines >= 1080:
		return 4
	case lines >= 720:
		return 3
	case lines >= 576:
		return 2
	default:
		return 1
	}
}

// matchTerm reports whether term occurs as whole word(s) in title.
func matchTerm(term, title string) bool {
	re, err := compileTerm(term)
	return err == nil && re != nil && re.MatchString(title)
}

func compileTerm(term string) (*regexp.Regexp, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, nil
	}
	if len(term) > 2 && strings.HasPrefix(term, "/") && strings.HasSuffix(term, "/") {
		return regexp.Compile("(?i)" + term[1:len(term)-1])
	}
	words := strings.FieldsFunc(term, func(r rune) bool { return r == ' ' || r == '.' || r == '_' })
	for i, w := range words {
		words[i] = regexp.QuoteMeta(w)
	}
	// Words are separated by dots, underscores, spaces or hyphens in names.
	return regexp.Compile(`(?i)(?:^|[^\pL\pN])` + strings.Join(words, `[ ._-]`) + `(?:$|[^\pL\pN])`)
}

// MatchesMovie reports whether a release name belongs to a movie, for
// results of free-text searches. One year of tolerance covers releases
// dated by their local premiere.
func MatchesMovie(info Info, titles []string, year int) bool {
	if info.Year != 0 && year != 0 && (info.Year < year-1 || info.Year > year+1) {
		return false
	}
	got := Normalize(info.Title)
	for _, t := range titles {
		if t != "" && Normalize(t) == got {
			return true
		}
	}
	return false
}

// Fingerprint identifies a release across indexers: the same post appears
// with the same name and (roughly) the same size everywhere.
func Fingerprint(title string, size int64) string {
	return fmt.Sprintf("%s|%d", Normalize(extension.ReplaceAllString(title, "")), size>>20)
}
