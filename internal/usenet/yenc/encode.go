package yenc

import (
	"bufio"
	"fmt"
	"hash/crc32"
	"io"
)

// DefaultLineLength is the encoded line length used by most posters.
const DefaultLineLength = 128

// Encode writes p as a yEnc article body with CRLF line endings. NNTP
// dot-stuffing is left to the transport. Multi-part posts (p.Total > 1) get a
// =ypart line and a part CRC; single-part posts a whole-file CRC.
//
// The project only downloads; Encode exists for tests and the fake NNTP server.
func Encode(w io.Writer, p Part, lineLength int) error {
	if lineLength <= 0 {
		lineLength = DefaultLineLength
	}
	bw := bufio.NewWriter(w)
	multi := p.Total > 1
	if multi {
		fmt.Fprintf(bw, "=ybegin part=%d total=%d line=%d size=%d name=%s\r\n", p.Number, p.Total, lineLength, p.FileSize, p.Name)
		fmt.Fprintf(bw, "=ypart begin=%d end=%d\r\n", p.Begin+1, p.Begin+int64(len(p.Data)))
	} else {
		fmt.Fprintf(bw, "=ybegin line=%d size=%d name=%s\r\n", lineLength, p.FileSize, p.Name)
	}

	col := 0
	for i, b := range p.Data {
		c := b + 42
		last := i == len(p.Data)-1 || col >= lineLength-1
		escape := c == 0 || c == '\n' || c == '\r' || c == '=' ||
			((c == '\t' || c == ' ') && (col == 0 || last)) ||
			(c == '.' && col == 0)
		if escape {
			bw.WriteByte('=')
			c += 64
			col++
		}
		bw.WriteByte(c)
		col++
		if col >= lineLength {
			bw.WriteString("\r\n")
			col = 0
		}
	}
	if col > 0 {
		bw.WriteString("\r\n")
	}

	crc := crc32.ChecksumIEEE(p.Data)
	if multi {
		fmt.Fprintf(bw, "=yend size=%d part=%d pcrc32=%08x\r\n", len(p.Data), p.Number, crc)
	} else {
		fmt.Fprintf(bw, "=yend size=%d crc32=%08x\r\n", len(p.Data), crc)
	}
	return bw.Flush()
}
