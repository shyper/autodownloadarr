// Package yenc decodes and encodes yEnc, the binary encoding used for Usenet
// posts (http://www.yenc.org/yenc-draft.1.3.txt).
package yenc

import (
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
)

// Decoding errors. A part failing with any of them is corrupt on that server.
var (
	ErrNoHeader  = errors.New("yenc: no =ybegin header")
	ErrTruncated = errors.New("yenc: no =yend trailer")
	ErrMalformed = errors.New("yenc: malformed header")
	ErrSize      = errors.New("yenc: size mismatch")
	ErrCRC       = errors.New("yenc: CRC32 mismatch")
)

// Part is one decoded article.
type Part struct {
	Name string
	// FileSize is the size of the whole file the part belongs to.
	FileSize int64
	Number   int // 1-based; 1 for single-part posts
	Total    int // 0 if not given
	// Begin is the zero-based offset of Data within the file.
	Begin int64
	Data  []byte
}

const (
	stateIdle   = iota // waiting for =ybegin
	stateHeader        // after =ybegin, =ypart may follow
	stateData
	stateDone // after =yend
)

// Decoder decodes one article body fed line by line, without line endings
// and with NNTP dot-stuffing already removed. It is reused via Reset so that
// the data buffer is allocated only once per worker.
type Decoder struct {
	part    Part
	state   int
	escape  bool
	hasPart bool
	end     int64 // =ypart end (1-based, inclusive)

	endSize         int64
	pcrc, crc       uint32
	hasPCRC, hasCRC bool
	hasEndSize      bool
}

// NewDecoder returns a decoder ready for the first article.
func NewDecoder() *Decoder { return &Decoder{} }

// Reset prepares the decoder for the next article, keeping its buffer.
func (d *Decoder) Reset() {
	*d = Decoder{part: Part{Data: d.part.Data[:0]}}
}

// Part returns the decoded part. Data is only valid until the next Reset.
func (d *Decoder) Part() Part { return d.part }

// Line consumes one line of the article body.
func (d *Decoder) Line(line []byte) error {
	switch {
	case d.state == stateDone:
		return nil
	case d.state == stateIdle:
		if bytes.HasPrefix(line, []byte("=ybegin ")) {
			return d.parseBegin(line[len("=ybegin "):])
		}
		return nil // tolerate text before the header
	case d.state == stateHeader && bytes.HasPrefix(line, []byte("=ypart ")):
		return d.parsePart(line[len("=ypart "):])
	case bytes.HasPrefix(line, []byte("=yend")):
		d.state = stateDone
		return d.parseEnd(line[len("=yend"):])
	}
	d.state = stateData
	d.decode(line)
	return nil
}

func (d *Decoder) decode(line []byte) {
	data := d.part.Data
	if free := cap(data) - len(data); free < len(line) {
		data = append(data[:cap(data)], make([]byte, len(line)-free+cap(data))...)[:len(data)]
	}
	n := len(data)
	data = data[:n+len(line)]
	esc := d.escape
	// Escapes are rare, so decode the runs between them in a tight loop.
	for len(line) > 0 {
		if esc {
			data[n] = line[0] - 106 // undo the +64 escape offset and +42
			n++
			line = line[1:]
			esc = false
			continue
		}
		i := bytes.IndexByte(line, '=')
		if i < 0 {
			i = len(line)
		}
		dst := data[n : n+i]
		for j, c := range line[:i] {
			dst[j] = c - 42
		}
		n += i
		if i < len(line) {
			esc = true
			i++
		}
		line = line[i:]
	}
	d.escape = esc
	d.part.Data = data[:n]
}

// Finish validates the article after its last line.
func (d *Decoder) Finish() error {
	switch d.state {
	case stateIdle:
		return ErrNoHeader
	case stateHeader, stateData:
		return ErrTruncated
	}
	p := &d.part
	size := int64(len(p.Data))
	if d.hasPart {
		if want := d.end - p.Begin; size != want {
			return fmt.Errorf("%w: decoded %d bytes, =ypart says %d", ErrSize, size, want)
		}
	} else if size != p.FileSize {
		return fmt.Errorf("%w: decoded %d bytes, =ybegin says %d", ErrSize, size, p.FileSize)
	}
	if d.hasEndSize && d.endSize != size {
		return fmt.Errorf("%w: decoded %d bytes, =yend says %d", ErrSize, size, d.endSize)
	}
	if p.FileSize > 0 && p.Begin+size > p.FileSize {
		return fmt.Errorf("%w: part ends at %d beyond file size %d", ErrSize, p.Begin+size, p.FileSize)
	}

	var want uint32
	var check bool
	switch {
	case d.hasPCRC:
		want, check = d.pcrc, true
	case d.hasCRC && !d.hasPart:
		want, check = d.crc, true // single-part: crc32 covers the part
	}
	if check {
		if got := crc32.ChecksumIEEE(p.Data); got != want {
			return fmt.Errorf("%w: got %08x, want %08x", ErrCRC, got, want)
		}
	}
	return nil
}

func (d *Decoder) parseBegin(rest []byte) error {
	fields, name := splitFields(rest, true)
	size, err := intField(fields, "size", true)
	if err != nil {
		return err
	}
	part, err := intField(fields, "part", false)
	if err != nil {
		return err
	}
	total, err := intField(fields, "total", false)
	if err != nil {
		return err
	}
	if size < 0 || part < 0 || total < 0 {
		return ErrMalformed
	}
	d.part.Name = name
	d.part.FileSize = size
	d.part.Number = max(int(part), 1)
	d.part.Total = int(total)
	d.state = stateHeader
	if est := size; est > 0 && cap(d.part.Data) == 0 {
		d.part.Data = make([]byte, 0, min(est, 4<<20))
	}
	return nil
}

func (d *Decoder) parsePart(rest []byte) error {
	fields, _ := splitFields(rest, false)
	begin, err := intField(fields, "begin", true)
	if err != nil {
		return err
	}
	end, err := intField(fields, "end", true)
	if err != nil {
		return err
	}
	if begin < 1 || end < begin-1 {
		return fmt.Errorf("%w: =ypart begin=%d end=%d", ErrMalformed, begin, end)
	}
	d.hasPart = true
	d.part.Begin = begin - 1
	d.end = end
	d.state = stateData
	return nil
}

func (d *Decoder) parseEnd(rest []byte) error {
	fields, _ := splitFields(rest, false)
	if v, ok := fields["size"]; ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("%w: =yend size", ErrMalformed)
		}
		d.endSize, d.hasEndSize = n, true
	}
	var err error
	if d.pcrc, d.hasPCRC, err = crcField(fields, "pcrc32"); err != nil {
		return err
	}
	if d.crc, d.hasCRC, err = crcField(fields, "crc32"); err != nil {
		return err
	}
	return nil
}

// splitFields parses "key=value" pairs separated by spaces. With withName,
// "name=" takes the rest of the line, as file names may contain spaces.
func splitFields(b []byte, withName bool) (map[string]string, string) {
	fields := make(map[string]string, 5)
	var name string
	for len(b) > 0 {
		b = bytes.TrimLeft(b, " ")
		if withName && bytes.HasPrefix(b, []byte("name=")) {
			name = string(bytes.TrimSpace(b[len("name="):]))
			break
		}
		token := b
		if i := bytes.IndexByte(b, ' '); i >= 0 {
			token, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		if k, v, ok := bytes.Cut(token, []byte("=")); ok {
			fields[string(k)] = string(v)
		}
	}
	return fields, name
}

func intField(fields map[string]string, key string, required bool) (int64, error) {
	v, ok := fields[key]
	if !ok {
		if required {
			return 0, fmt.Errorf("%w: %s missing", ErrMalformed, key)
		}
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s=%q", ErrMalformed, key, v)
	}
	return n, nil
}

func crcField(fields map[string]string, key string) (uint32, bool, error) {
	v, ok := fields[key]
	if !ok {
		return 0, false, nil
	}
	n, err := strconv.ParseUint(v, 16, 32)
	if err != nil {
		return 0, false, fmt.Errorf("%w: %s=%q", ErrMalformed, key, v)
	}
	return uint32(n), true, nil
}
