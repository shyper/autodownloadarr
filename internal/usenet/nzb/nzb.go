// Package nzb parses NZB files: the list of Usenet articles that make up a
// release.
package nzb

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxSize is the largest NZB accepted by Parse.
const MaxSize = 64 << 20

// ErrEmpty is returned for NZBs without any downloadable file.
var ErrEmpty = errors.New("nzb: no files with segments")

// NZB is a parsed NZB document.
type NZB struct {
	// Meta holds the <head> entries, e.g. "title", "password", "category".
	Meta  map[string][]string
	Files []File
}

// File is one posted file, made up of segments (articles).
type File struct {
	Poster  string
	Date    time.Time
	Subject string
	// Filename is parsed from the subject and may be empty.
	Filename string
	Groups   []string
	// Segments are sorted by number without duplicates.
	Segments []Segment
}

// Segment is a single article.
type Segment struct {
	Number int
	// Bytes is the encoded article size as given by the NZB.
	Bytes     int64
	MessageID string // without angle brackets
}

// Size is the encoded size of all segments.
func (f File) Size() int64 {
	var n int64
	for _, s := range f.Segments {
		n += s.Bytes
	}
	return n
}

// Size is the encoded size of all files.
func (n *NZB) Size() int64 {
	var total int64
	for _, f := range n.Files {
		total += f.Size()
	}
	return total
}

// Passwords returns the archive passwords from <meta type="password">.
func (n *NZB) Passwords() []string {
	return n.Meta["password"]
}

type xmlNZB struct {
	XMLName xml.Name `xml:"nzb"`
	Meta    []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"head>meta"`
	Files []struct {
		Poster   string   `xml:"poster,attr"`
		Date     int64    `xml:"date,attr"`
		Subject  string   `xml:"subject,attr"`
		Groups   []string `xml:"groups>group"`
		Segments []struct {
			Bytes  int64  `xml:"bytes,attr"`
			Number int    `xml:"number,attr"`
			ID     string `xml:",chardata"`
		} `xml:"segments>segment"`
	} `xml:"file"`
}

// Parse reads an NZB document.
func Parse(r io.Reader) (*NZB, error) {
	dec := xml.NewDecoder(io.LimitReader(r, MaxSize))
	dec.CharsetReader = charsetReader
	var doc xmlNZB
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("nzb: %w", err)
	}

	n := &NZB{Meta: make(map[string][]string)}
	for _, m := range doc.Meta {
		key := strings.ToLower(strings.TrimSpace(m.Type))
		if v := strings.TrimSpace(m.Value); key != "" && v != "" {
			n.Meta[key] = append(n.Meta[key], v)
		}
	}
	for _, xf := range doc.Files {
		f := File{
			Poster:   strings.TrimSpace(xf.Poster),
			Subject:  strings.TrimSpace(xf.Subject),
			Groups:   xf.Groups,
			Segments: make([]Segment, 0, len(xf.Segments)),
		}
		if xf.Date > 0 {
			f.Date = time.Unix(xf.Date, 0).UTC()
		}
		f.Filename = FilenameFromSubject(f.Subject)
		for _, xs := range xf.Segments {
			id := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(xs.ID), "<"), ">")
			if id == "" || strings.ContainsAny(id, " \t\r\n<>") {
				continue
			}
			f.Segments = append(f.Segments, Segment{Number: xs.Number, Bytes: xs.Bytes, MessageID: id})
		}
		if len(f.Segments) == 0 {
			continue
		}
		slices.SortStableFunc(f.Segments, func(a, b Segment) int { return a.Number - b.Number })
		f.Segments = slices.CompactFunc(f.Segments, func(a, b Segment) bool { return a.Number == b.Number })
		n.Files = append(n.Files, f)
	}
	if len(n.Files) == 0 {
		return nil, ErrEmpty
	}
	return n, nil
}

var (
	quotedName  = regexp.MustCompile(`"([^"]+)"`)
	yencSuffix  = regexp.MustCompile(`(?i)\s*\byenc\b.*$`)
	partCounter = regexp.MustCompile(`\s*[\[(]\d+/\d+[\])]\s*`)
)

// FilenameFromSubject extracts the file name from a typical posting subject
// such as `[01/20] - "Movie.2024.part01.rar" yEnc (1/50)`.
func FilenameFromSubject(subject string) string {
	if m := quotedName.FindStringSubmatch(subject); m != nil {
		return strings.TrimSpace(m[1])
	}
	s := yencSuffix.ReplaceAllString(subject, "")
	s = partCounter.ReplaceAllString(s, " ")
	fields := strings.Fields(s)
	for i := len(fields) - 1; i >= 0; i-- {
		if f := strings.Trim(fields[i], "[]-"); strings.Contains(f, ".") {
			return f
		}
	}
	return ""
}

var filenamePassword = regexp.MustCompile(`\{\{(.+?)\}\}`)

// PasswordFromFilename returns the password embedded in an NZB file name
// using the common convention "Release.Name{{password}}.nzb".
func PasswordFromFilename(name string) string {
	if m := filenamePassword.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return ""
}

// charsetReader supports the encodings found in real-world NZBs. Windows-1252
// is decoded as Latin-1, which differs only in rarely used punctuation.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		return &latin1Reader{r: bufio.NewReader(input)}, nil
	}
	return nil, fmt.Errorf("unsupported charset %q", charset)
}

type latin1Reader struct {
	r       *bufio.Reader
	pending []byte
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(l.pending) > 0 {
			c := copy(p[n:], l.pending)
			l.pending = l.pending[c:]
			n += c
			continue
		}
		b, err := l.r.ReadByte()
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		if b < utf8.RuneSelf {
			p[n] = b
			n++
			continue
		}
		l.pending = utf8.AppendRune(l.pending[:0], rune(b))
	}
	return n, nil
}

// WriteXML writes n as an NZB document.
func (n *NZB) WriteXML(w io.Writer) error {
	type xmlSegment struct {
		Bytes  int64  `xml:"bytes,attr"`
		Number int    `xml:"number,attr"`
		ID     string `xml:",chardata"`
	}
	type xmlFile struct {
		Poster   string       `xml:"poster,attr"`
		Date     int64        `xml:"date,attr"`
		Subject  string       `xml:"subject,attr"`
		Groups   []string     `xml:"groups>group"`
		Segments []xmlSegment `xml:"segments>segment"`
	}
	type xmlMeta struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	}
	doc := struct {
		XMLName xml.Name  `xml:"http://www.newzbin.com/DTD/2003/nzb nzb"`
		Meta    []xmlMeta `xml:"head>meta"`
		Files   []xmlFile `xml:"file"`
	}{}
	keys := make([]string, 0, len(n.Meta))
	for k := range n.Meta {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		for _, v := range n.Meta[k] {
			doc.Meta = append(doc.Meta, xmlMeta{Type: k, Value: v})
		}
	}
	for _, f := range n.Files {
		xf := xmlFile{Poster: f.Poster, Subject: f.Subject, Groups: f.Groups}
		if !f.Date.IsZero() {
			xf.Date = f.Date.Unix()
		}
		for _, s := range f.Segments {
			xf.Segments = append(xf.Segments, xmlSegment{Bytes: s.Bytes, Number: s.Number, ID: s.MessageID})
		}
		doc.Files = append(doc.Files, xf)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	return enc.Encode(doc)
}
