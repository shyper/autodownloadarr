package yenc

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
)

// decodeAll feeds an encoded body to d line by line.
func decodeAll(t testing.TB, d *Decoder, body []byte) error {
	t.Helper()
	d.Reset()
	for line := range bytes.SplitSeq(bytes.TrimSuffix(body, []byte("\r\n")), []byte("\r\n")) {
		if err := d.Line(line); err != nil {
			return err
		}
	}
	return d.Finish()
}

func encode(t testing.TB, p Part) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, p, DefaultLineLength); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func randomBytes(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestRoundTripMultiPart(t *testing.T) {
	file := randomBytes(10_000, 1)
	d := NewDecoder()
	for i, begin := 0, 0; begin < len(file); i++ {
		end := min(begin+3000, len(file))
		body := encode(t, Part{Name: "My File (2024).mkv", FileSize: int64(len(file)), Number: i + 1, Total: 4, Begin: int64(begin), Data: file[begin:end]})
		if err := decodeAll(t, d, body); err != nil {
			t.Fatalf("part %d: %v", i+1, err)
		}
		p := d.Part()
		if p.Name != "My File (2024).mkv" || p.Number != i+1 || p.Total != 4 || p.Begin != int64(begin) || p.FileSize != 10_000 {
			t.Errorf("part %d header = %+v", i+1, p)
		}
		if !bytes.Equal(p.Data, file[begin:end]) {
			t.Fatalf("part %d data differs", i+1)
		}
		begin = end
	}
}

func TestRoundTripSinglePartAllByteValues(t *testing.T) {
	data := make([]byte, 0, 256*3)
	for range 3 {
		for b := range 256 {
			data = append(data, byte(b))
		}
	}
	d := NewDecoder()
	if err := decodeAll(t, d, encode(t, Part{Name: "x.bin", FileSize: int64(len(data)), Data: data})); err != nil {
		t.Fatal(err)
	}
	if p := d.Part(); !bytes.Equal(p.Data, data) || p.Begin != 0 || p.Number != 1 {
		t.Errorf("unexpected part %+v", p)
	}
}

func TestEncodedLinesAreSafe(t *testing.T) {
	body := encode(t, Part{Name: "x", FileSize: 4096, Data: randomBytes(4096, 7)})
	for _, line := range strings.Split(string(body), "\r\n") {
		if strings.ContainsAny(line, "\x00\r\n") {
			t.Fatalf("unescaped control byte in %q", line)
		}
		if strings.HasPrefix(line, ".") {
			t.Fatalf("line starts with a dot: %q", line)
		}
	}
}

func TestErrors(t *testing.T) {
	good := encode(t, Part{Name: "f", FileSize: 1000, Number: 1, Total: 2, Data: randomBytes(500, 3)})
	d := NewDecoder()

	corrupt := bytes.Clone(good)
	i := bytes.Index(corrupt, []byte("\r\n")) + 2
	i += bytes.Index(corrupt[i:], []byte("\r\n")) + 2 // first data line
	corrupt[i+5] ^= 0x01
	if err := decodeAll(t, d, corrupt); !errors.Is(err, ErrCRC) {
		t.Errorf("flipped bit: %v", err)
	}

	truncated := good[:bytes.LastIndex(good[:len(good)-2], []byte("\r\n"))+2]
	if err := decodeAll(t, d, truncated); !errors.Is(err, ErrTruncated) {
		t.Errorf("missing =yend: %v", err)
	}

	if err := decodeAll(t, d, []byte("just some text\r\n")); !errors.Is(err, ErrNoHeader) {
		t.Errorf("no header: %v", err)
	}

	if err := decodeAll(t, d, []byte("=ybegin line=128 name=x\r\n")); !errors.Is(err, ErrMalformed) {
		t.Errorf("missing size: %v", err)
	}

	short := bytes.Replace(good, []byte("end=500"), []byte("end=600"), 1)
	if err := decodeAll(t, d, short); !errors.Is(err, ErrSize) {
		t.Errorf("size mismatch: %v", err)
	}

	// After errors the decoder is still usable.
	if err := decodeAll(t, d, good); err != nil {
		t.Errorf("reuse after error: %v", err)
	}
}

func TestNameWithSpacesAndEquals(t *testing.T) {
	d := NewDecoder()
	body := "=ybegin line=128 size=1 name= a=b c.txt \r\n" + string([]byte{'a' + 42}) + "\r\n=yend size=1\r\n"
	if err := decodeAll(t, d, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if got := d.Part().Name; got != "a=b c.txt" {
		t.Errorf("name = %q", got)
	}
}

func BenchmarkDecode(b *testing.B) {
	data := randomBytes(750_000, 42) // typical article size
	body := encode(b, Part{Name: "bench.bin", FileSize: 10 << 20, Number: 1, Total: 14, Data: data})
	lines := bytes.Split(bytes.TrimSuffix(body, []byte("\r\n")), []byte("\r\n"))
	d := NewDecoder()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		d.Reset()
		for _, l := range lines {
			if err := d.Line(l); err != nil {
				b.Fatal(err)
			}
		}
		if err := d.Finish(); err != nil {
			b.Fatal(err)
		}
	}
}
