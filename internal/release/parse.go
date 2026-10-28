// Package release parses release names and decides which release of a
// movie to download, based on a quality profile.
package release

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Source is where a release was ripped from.
type Source string

// Sources, roughly from best to worst.
const (
	SourceUnknown  Source = ""
	SourceRemux    Source = "remux"
	SourceBluRay   Source = "bluray"
	SourceWebDL    Source = "webdl"
	SourceWebRip   Source = "webrip"
	SourceHDTV     Source = "hdtv"
	SourceDVD      Source = "dvd"
	SourceScreener Source = "screener"
	SourceTelecine Source = "telecine"
	SourceTelesync Source = "telesync"
	SourceCam      Source = "cam"
)

// LowQuality reports whether s is a cinema recording or pre-release copy.
func (s Source) LowQuality() bool {
	switch s {
	case SourceCam, SourceTelesync, SourceTelecine, SourceScreener:
		return true
	}
	return false
}

// Info is what can be read from a release name.
type Info struct {
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	// Resolution is the number of lines (2160, 1080, 720, 576, 480); 0 if unknown.
	Resolution int    `json:"resolution,omitempty"`
	Source     Source `json:"source,omitempty"`
	Codec      string `json:"codec,omitempty"` // h264, h265, av1, xvid
	HDR        bool   `json:"hdr,omitempty"`
	// Languages are ISO 639-1 codes. Scene releases without a language tag
	// are English; LanguagesGuessed is set then.
	Languages        []string `json:"languages,omitempty"`
	LanguagesGuessed bool     `json:"languagesGuessed,omitempty"`
	Group            string   `json:"group,omitempty"`
	Proper           bool     `json:"proper,omitempty"` // PROPER, REPACK, RERIP
	// Season and Episodes are set for TV releases; a season pack has a
	// season but no episodes.
	Season   int   `json:"season,omitempty"`
	Episodes []int `json:"episodes,omitempty"`
}

// SeasonPack reports whether the release contains a whole season.
func (i Info) SeasonPack() bool { return i.Season > 0 && len(i.Episodes) == 0 }

var (
	extension  = regexp.MustCompile(`(?i)\.(nzb|mkv|mp4|avi|rar)$`)
	separators = regexp.MustCompile(`[._\s]+`)
	yearToken  = regexp.MustCompile(`^\(?((?:19|20)\d{2})\)?$`)
	groupRe    = regexp.MustCompile(`-([A-Za-z0-9]+)(?:\[[^\]]*\])?$`)

	episodeToken = regexp.MustCompile(`(?i)^S(\d{1,2})E(\d{1,3})((?:-?E?\d{1,3})*)$`)
	moreEpisodes = regexp.MustCompile(`(?i)E?(\d{1,3})`)
	crossToken   = regexp.MustCompile(`(?i)^(\d{1,2})x(\d{2,3})$`)
	seasonToken  = regexp.MustCompile(`(?i)^S(\d{1,2})$`)
	numberToken  = regexp.MustCompile(`^\d{1,2}$`)
)

var resolutions = map[string]int{
	"2160p": 2160, "4k": 2160, "uhd": 2160,
	"1080p": 1080, "1080i": 1080,
	"720p": 720,
	"576p": 576, "576i": 576,
	"480p": 480, "480i": 480,
}

// sourceTokens map lower-case tokens to sources; the first match in the
// name wins, except that remux beats bluray.
var sourceTokens = map[string]Source{
	"remux":  SourceRemux,
	"bluray": SourceBluRay, "blu-ray": SourceBluRay, "bdrip": SourceBluRay, "brrip": SourceBluRay, "bd": SourceBluRay,
	"web-dl": SourceWebDL, "webdl": SourceWebDL, "web": SourceWebDL, "amzn": SourceWebDL, "nf": SourceWebDL, "dsnp": SourceWebDL,
	"webrip": SourceWebRip, "web-rip": SourceWebRip,
	"hdtv": SourceHDTV, "pdtv": SourceHDTV, "dsr": SourceHDTV,
	"dvdrip": SourceDVD, "dvd": SourceDVD, "dvd9": SourceDVD, "dvd5": SourceDVD,
	"scr": SourceScreener, "screener": SourceScreener, "dvdscr": SourceScreener,
	"tc": SourceTelecine, "telecine": SourceTelecine,
	"ts": SourceTelesync, "telesync": SourceTelesync, "hdts": SourceTelesync, "pdvd": SourceTelesync,
	"cam": SourceCam, "hdcam": SourceCam, "camrip": SourceCam,
}

var codecTokens = map[string]string{
	"x264": "h264", "h264": "h264", "h.264": "h264", "avc": "h264",
	"x265": "h265", "h265": "h265", "h.265": "h265", "hevc": "h265",
	"av1":  "av1",
	"xvid": "xvid", "divx": "xvid",
}

var languageTokens = map[string]string{
	"german": "de", "deutsch": "de", "ger": "de",
	"english": "en", "eng": "en",
	"french": "fr", "truefrench": "fr", "vff": "fr",
	"spanish": "es", "castellano": "es",
	"italian": "it", "ita": "it",
	"dutch": "nl", "flemish": "nl",
	"polish": "pl", "russian": "ru", "turkish": "tr",
	"danish": "da", "swedish": "sv", "norwegian": "no", "finnish": "fi",
	"japanese": "ja", "korean": "ko", "chinese": "zh",
}

// Parse reads the attributes of a release name such as
// "Dune.Part.Two.2024.German.DL.1080p.WEB.h264-GROUP".
func Parse(name string) Info {
	var info Info
	name = extension.ReplaceAllString(strings.TrimSpace(name), "")
	if m := groupRe.FindStringSubmatch(name); m != nil {
		info.Group = m[1]
	}
	tokens := tokenize(name)

	// The year is the last year-like token that is not the first token
	// (titles such as "2012" are movies too).
	yearAt := -1
	for i := len(tokens) - 1; i > 0; i-- {
		if m := yearToken.FindStringSubmatch(tokens[i]); m != nil {
			info.Year, _ = strconv.Atoi(m[1])
			yearAt = i
			break
		}
	}

	firstTag := len(tokens)
	dual := false
	for i, t := range tokens {
		lt := strings.ToLower(t)
		tag := true
		switch {
		case resolutions[lt] != 0:
			info.Resolution = max(info.Resolution, resolutions[lt])
		case sourceTokens[lt] != "":
			if s := sourceTokens[lt]; info.Source == "" || s == SourceRemux {
				info.Source = s
			}
		case codecTokens[lt] != "":
			info.Codec = codecTokens[lt]
		case lt == "hdr" || lt == "hdr10" || lt == "hdr10+" || lt == "dv" || lt == "dovi":
			info.HDR = true
		case languageTokens[lt] != "":
			if l := languageTokens[lt]; !slices.Contains(info.Languages, l) {
				info.Languages = append(info.Languages, l)
			}
		case lt == "dl" || lt == "dual":
			dual = true
		case lt == "proper" || lt == "repack" || lt == "rerip":
			info.Proper = true
		case episodeToken.MatchString(t) && info.Season == 0:
			m := episodeToken.FindStringSubmatch(t)
			info.Season, _ = strconv.Atoi(m[1])
			first, _ := strconv.Atoi(m[2])
			info.Episodes = []int{first}
			for _, e := range moreEpisodes.FindAllStringSubmatch(m[3], -1) {
				if n, _ := strconv.Atoi(e[1]); n > info.Episodes[len(info.Episodes)-1] {
					// "E01-03" means 1, 2 and 3.
					for k := info.Episodes[len(info.Episodes)-1] + 1; k <= n; k++ {
						info.Episodes = append(info.Episodes, k)
					}
				}
			}
		case crossToken.MatchString(t) && info.Season == 0:
			m := crossToken.FindStringSubmatch(t)
			info.Season, _ = strconv.Atoi(m[1])
			ep, _ := strconv.Atoi(m[2])
			info.Episodes = []int{ep}
		case seasonToken.MatchString(t) && info.Season == 0:
			info.Season, _ = strconv.Atoi(seasonToken.FindStringSubmatch(t)[1])
		case (lt == "season" || lt == "staffel") && i+1 < len(tokens) && numberToken.MatchString(tokens[i+1]) && info.Season == 0:
			info.Season, _ = strconv.Atoi(tokens[i+1])
		default:
			tag = false
		}
		if tag && i < firstTag && i > 0 {
			firstTag = i
		}
	}
	if dual && !slices.Contains(info.Languages, "en") {
		// German scene: "German DL" is German plus the original (English).
		info.Languages = append(info.Languages, "en")
	}
	if len(info.Languages) == 0 {
		info.Languages = []string{"en"}
		info.LanguagesGuessed = true
	}

	end := firstTag
	if yearAt > 0 && yearAt < end {
		end = yearAt
	}
	info.Title = strings.TrimSpace(strings.Join(tokens[:end], " "))
	return info
}

// tokenize splits a release name at dots, underscores and spaces. Hyphens
// stay inside tokens ("WEB-DL"), except before the trailing group.
func tokenize(name string) []string {
	if i := strings.LastIndex(name, "-"); i > 0 && !strings.ContainsAny(name[i:], ". _") {
		name = name[:i]
	}
	var out []string
	for _, t := range separators.Split(name, -1) {
		if t = strings.Trim(t, "[]()"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Normalize lower-cases a title and keeps only letters and digits, so that
// "Dune: Part Two" and "Dune.Part.Two" compare equal.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == 'ä':
			b.WriteString("ae")
		case r == 'ö':
			b.WriteString("oe")
		case r == 'ü':
			b.WriteString("ue")
		case r == 'ß':
			b.WriteString("ss")
		case r == '&':
			b.WriteString("and")
		case r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}
