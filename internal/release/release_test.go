package release

import (
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		want Info
	}{
		{"Dune.Part.Two.2024.German.DL.1080p.WEB.h264-GROUP", Info{
			Title: "Dune Part Two", Year: 2024, Resolution: 1080, Source: SourceWebDL, Codec: "h264",
			Languages: []string{"de", "en"}, Group: "GROUP",
		}},
		{"The.Matrix.1999.720p.BluRay.x264-SiNNERS", Info{
			Title: "The Matrix", Year: 1999, Resolution: 720, Source: SourceBluRay, Codec: "h264",
			Languages: []string{"en"}, LanguagesGuessed: true, Group: "SiNNERS",
		}},
		{"2012.2009.GERMAN.DL.2160p.UHD.BluRay.REMUX.HDR.HEVC-FuN", Info{
			Title: "2012", Year: 2009, Resolution: 2160, Source: SourceRemux, Codec: "h265", HDR: true,
			Languages: []string{"de", "en"}, Group: "FuN",
		}},
		{"Some Movie (2023) German 720p WEBRip x265 PROPER-ABC.nzb", Info{
			Title: "Some Movie", Year: 2023, Resolution: 720, Source: SourceWebRip, Codec: "h265",
			Languages: []string{"de"}, Group: "ABC", Proper: true,
		}},
		{"Bad.Movie.2025.HDCAM.x264-XYZ", Info{
			Title: "Bad Movie", Year: 2025, Source: SourceCam, Codec: "h264",
			Languages: []string{"en"}, LanguagesGuessed: true, Group: "XYZ",
		}},
		{"Spider-Man.No.Way.Home.2021.German.AC3.WEB-DL.720p", Info{
			Title: "Spider-Man No Way Home", Year: 2021, Resolution: 720, Source: SourceWebDL,
			Languages: []string{"de"},
		}},
	}
	for _, c := range cases {
		got := Parse(c.name)
		if got.Title != c.want.Title || got.Year != c.want.Year || got.Resolution != c.want.Resolution ||
			got.Source != c.want.Source || got.Codec != c.want.Codec || got.HDR != c.want.HDR ||
			!slices.Equal(got.Languages, c.want.Languages) || got.LanguagesGuessed != c.want.LanguagesGuessed ||
			got.Group != c.want.Group || got.Proper != c.want.Proper {
			t.Errorf("Parse(%q)\n got  %+v\n want %+v", c.name, got, c.want)
		}
	}
}

func TestEvaluate(t *testing.T) {
	p := DefaultProfile()
	p.MaxResolution = 720
	p.RequiredWords = []string{"German"}
	p.ForbiddenWords = []string{"LD", "/\\bMD\\b/"}
	p.PreferredWords = []WordScore{{Term: "DL", Score: 100}}

	const gib = 1 << 30
	cases := []struct {
		title  string
		size   int64
		reject string
	}{
		{"Movie.2024.German.DL.720p.WEB.h264-G", 2 * gib, ""},
		{"Movie.2024.German.720p.BluRay.x264-G", 3 * gib, ""},
		{"Movie.2024.German.DL.1080p.WEB.h264-G", 4 * gib, RejectResolutionHigh},
		{"Movie.2024.720p.WEB.h264-G", 2 * gib, RejectRequired},
		{"Movie.2024.Germany.720p.WEB.h264-G", 2 * gib, RejectRequired},
		{"Movie.2024.German.LD.720p.WEB.h264-G", 2 * gib, RejectForbidden},
		{"Movie.2024.German.MD.720p.WEB.h264-G", 2 * gib, RejectForbidden},
		{"Movie.2024.German.TS.720p.x264-G", 2 * gib, RejectLowQuality},
		{"Movie.2024.German.720p.WEB.h264-G", 100 << 20, RejectTooSmall},
		{"Movie.2024.German.720p.WEB.h264-G", 30 * gib, RejectTooLarge},
	}
	scores := map[string]int{}
	for _, c := range cases {
		d := p.Evaluate(Candidate{Title: c.title, Size: c.size}, 120)
		if code, _, _ := strings.Cut(d.Rejected, ":"); code != c.reject {
			t.Errorf("%s: rejected %q, want %q", c.title, d.Rejected, c.reject)
		}
		scores[c.title] = d.Score
	}
	dl, plain := scores["Movie.2024.German.DL.720p.WEB.h264-G"], scores["Movie.2024.German.720p.BluRay.x264-G"]
	if dl <= plain {
		t.Errorf("preferred word not applied: DL %d, BluRay %d", dl, plain)
	}
}

func TestEvaluateResolutionRanking(t *testing.T) {
	p := DefaultProfile() // max 1080p
	hd := p.Evaluate(Candidate{Title: "M.2024.1080p.WEB.h264-G"}, 0)
	sd := p.Evaluate(Candidate{Title: "M.2024.720p.BluRay.x264-G"}, 0)
	if hd.Score <= sd.Score {
		t.Errorf("1080p (%d) should beat 720p (%d) within the limit", hd.Score, sd.Score)
	}
}

func TestPasswordPolicy(t *testing.T) {
	p := DefaultProfile()
	c := Candidate{Title: "M.2024.720p.WEB.h264-G", Passworded: true}
	base := p.Evaluate(Candidate{Title: c.Title}, 0).Score

	for policy, want := range map[string]string{PasswordAllow: "", PasswordReject: RejectPassword, PasswordKnown: RejectPassword} {
		p.PasswordPolicy = policy
		if got := p.Evaluate(c, 0).Rejected; got != want {
			t.Errorf("%s: rejected %q, want %q", policy, got, want)
		}
	}
	p.PasswordPolicy = PasswordAvoid
	if d := p.Evaluate(c, 0); d.Rejected != "" || d.Score >= base {
		t.Errorf("avoid: %+v (base %d)", d, base)
	}
	p.PasswordPolicy = PasswordKnown
	c.PasswordKnown = true
	if d := p.Evaluate(c, 0); d.Rejected != "" {
		t.Errorf("known password rejected: %+v", d)
	}
}

func TestValidate(t *testing.T) {
	p := DefaultProfile()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.ForbiddenWords = []string{"/(unclosed/"}
	if p.Validate() == nil {
		t.Error("broken regex accepted")
	}
	p = DefaultProfile()
	p.MinResolution, p.MaxResolution = 1080, 720
	if p.Validate() == nil {
		t.Error("min above max accepted")
	}
}

func TestMatchesMovie(t *testing.T) {
	titles := []string{"Dune: Part Two", "Dune: Teil Zwei"}
	if !MatchesMovie(Parse("Dune.Teil.Zwei.2024.German.720p.WEB-G"), titles, 2024) {
		t.Error("alternative title not matched")
	}
	if MatchesMovie(Parse("Dune.2021.German.720p.WEB-G"), titles, 2024) {
		t.Error("other movie matched")
	}
	if MatchesMovie(Parse("Dune.Part.Two.2019.720p.WEB-G"), titles, 2024) {
		t.Error("wrong year matched")
	}
}

func TestFingerprint(t *testing.T) {
	a := Fingerprint("Movie.2024.German.720p-G", 2_000_000_000)
	b := Fingerprint("movie 2024 german 720p g.nzb", 2_000_100_000)
	if a != b {
		t.Errorf("%q != %q", a, b)
	}
}

func TestParseTV(t *testing.T) {
	cases := []struct {
		name     string
		title    string
		season   int
		episodes []int
		pack     bool
	}{
		{"Dark.S02E05.German.DL.720p.WEB.h264-GRP", "Dark", 2, []int{5}, false},
		{"The.Office.US.S03E01E02.720p.HDTV.x264-GRP", "The Office US", 3, []int{1, 2}, false},
		{"Show.Name.S01E01-E03.German.1080p-GRP", "Show Name", 1, []int{1, 2, 3}, false},
		{"Show 2x07 German 720p", "Show", 2, []int{7}, false},
		{"Babylon.Berlin.S04.German.DL.1080p.WEB.x264-GRP", "Babylon Berlin", 4, nil, true},
		{"Tatort Staffel 3 German 720p", "Tatort", 3, nil, true},
		{"Dune.Part.Two.2024.German.720p", "Dune Part Two", 0, nil, false},
	}
	for _, c := range cases {
		got := Parse(c.name)
		if got.Title != c.title || got.Season != c.season || !slices.Equal(got.Episodes, c.episodes) || got.SeasonPack() != c.pack {
			t.Errorf("Parse(%q) = title %q S%d E%v pack %v", c.name, got.Title, got.Season, got.Episodes, got.SeasonPack())
		}
	}
}
