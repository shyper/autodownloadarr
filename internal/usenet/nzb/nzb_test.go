package nzb

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const sample = `<?xml version="1.0" encoding="iso-8859-1"?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <head>
    <meta type="title">Some Movie 2024</meta>
    <meta type="password">geheim</meta>
    <meta type="Password">zweites</meta>
  </head>
  <file poster="poster &lt;p@example.com&gt;" date="1730000000" subject="[1/2] - &quot;Some.Movie.2024.German.720p.part01.rar&quot; yEnc (1/3)">
    <groups><group>alt.binaries.test</group></groups>
    <segments>
      <segment bytes="300" number="3">c@example</segment>
      <segment bytes="100" number="1">&lt;a@example&gt;</segment>
      <segment bytes="200" number="2">b@example</segment>
      <segment bytes="200" number="2">b-duplicate@example</segment>
      <segment bytes="10" number="4">  </segment>
    </segments>
  </file>
  <file poster="p" date="0" subject="Caf` + "\xe9" + `.par2 yEnc (1/1)">
    <segments><segment bytes="50" number="1">d@example</segment></segments>
  </file>
  <file poster="p" subject="no segments"><segments/></file>
</nzb>`

func TestParse(t *testing.T) {
	n, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := n.Meta["title"]; len(got) != 1 || got[0] != "Some Movie 2024" {
		t.Errorf("title = %v", got)
	}
	if got := n.Passwords(); len(got) != 2 || got[0] != "geheim" || got[1] != "zweites" {
		t.Errorf("passwords = %v", got)
	}
	if len(n.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(n.Files))
	}

	f := n.Files[0]
	if f.Filename != "Some.Movie.2024.German.720p.part01.rar" {
		t.Errorf("filename = %q", f.Filename)
	}
	if f.Poster != "poster <p@example.com>" || !f.Date.Equal(time.Unix(1730000000, 0)) {
		t.Errorf("poster/date = %q %v", f.Poster, f.Date)
	}
	if len(f.Groups) != 1 || f.Groups[0] != "alt.binaries.test" {
		t.Errorf("groups = %v", f.Groups)
	}
	want := []Segment{{1, 100, "a@example"}, {2, 200, "b@example"}, {3, 300, "c@example"}}
	if len(f.Segments) != len(want) {
		t.Fatalf("segments = %+v", f.Segments)
	}
	for i := range want {
		if f.Segments[i] != want[i] {
			t.Errorf("segment %d = %+v, want %+v", i, f.Segments[i], want[i])
		}
	}
	if f.Size() != 600 || n.Size() != 650 {
		t.Errorf("sizes = %d / %d", f.Size(), n.Size())
	}
	if n.Files[1].Filename != "Café.par2" {
		t.Errorf("latin1 filename = %q", n.Files[1].Filename)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse(strings.NewReader(`<nzb><file subject="x"><segments/></file></nzb>`)); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: %v", err)
	}
	if _, err := Parse(strings.NewReader(`not xml`)); err == nil {
		t.Error("garbage accepted")
	}
	if _, err := Parse(strings.NewReader(`<?xml version="1.0" encoding="koi8-r"?><nzb/>`)); err == nil {
		t.Error("unknown charset accepted")
	}
}

func TestFilenameFromSubject(t *testing.T) {
	cases := map[string]string{
		`"file.mkv" yEnc (1/10)`:                          "file.mkv",
		`[01/20] - "My Movie (2024).part01.rar" yEnc`:     "My Movie (2024).part01.rar",
		`Some.Release.Name.part03.rar (1/50)`:             "Some.Release.Name.part03.rar",
		`[3/7] Some.Release.vol00+01.par2 yEnc (1/2)`:     "Some.Release.vol00+01.par2",
		`Release - [02/15] - file.r00 yEnc (12/100) 5000`: "file.r00",
		`a1b2c3d4e5f6 yEnc (1/99)`:                        "",
	}
	for subject, want := range cases {
		if got := FilenameFromSubject(subject); got != want {
			t.Errorf("FilenameFromSubject(%q) = %q, want %q", subject, got, want)
		}
	}
}

func TestPasswordFromFilename(t *testing.T) {
	if got := PasswordFromFilename("Movie.2024.German{{s3cr3t}}.nzb"); got != "s3cr3t" {
		t.Errorf("got %q", got)
	}
	if got := PasswordFromFilename("Movie.2024.nzb"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestWriteXMLRoundTrip(t *testing.T) {
	n, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := n.WriteXML(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := Parse(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("parse written NZB: %v\n%s", err, buf.String())
	}
	if len(back.Files) != len(n.Files) || back.Files[0].Filename != n.Files[0].Filename ||
		len(back.Files[0].Segments) != 3 || len(back.Passwords()) != 2 {
		t.Errorf("round trip lost data: %+v", back)
	}
}

func TestPasswordsFromText(t *testing.T) {
	text := "Film.2024.German\nPasswort: s3cr3t!\n[PW: other-pw] und {{third}}\npass=x"
	got := PasswordsFromText(text)
	want := []string{"third", "s3cr3t!", "other-pw"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if pw := PasswordsFromText("Passwort: www.spam-site.com"); len(pw) != 0 {
		t.Errorf("URL taken as password: %q", pw)
	}
}

func TestLooksLikeSpam(t *testing.T) {
	if !LooksLikeSpam("Das Passwort findest du auf https://example.com/pw") {
		t.Error("spam not detected")
	}
	if !LooksLikeSpam("PW gibt es bei www.board.to") {
		t.Error("spam not detected")
	}
	if LooksLikeSpam("Passwort: geheim") || LooksLikeSpam("Visit https://example.com for more releases") {
		t.Error("false positive")
	}
}

func TestPasswordsFromSubjects(t *testing.T) {
	n := &NZB{Files: []File{{Subject: `[01/10] "x.part1.rar" yEnc (1/5) PW: subj3ct`}, {Subject: "y {{braces}}"}}}
	got := n.PasswordsFromSubjects()
	if len(got) != 2 || got[0] != "subj3ct" || got[1] != "braces" {
		t.Errorf("got %q", got)
	}
}
