// Package par2 reads PAR2 index files and runs repairs.
//
// The index tells the real names of obfuscated files (by the MD5 of their
// first 16 KiB) and the slice size, which determines how many recovery
// blocks a repair needs. The repair itself is done by par2cmdline (or the
// faster par2cmdline-turbo), which has to be installed.
package par2

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ErrNoTool is returned when par2cmdline is not installed.
var ErrNoTool = errors.New("par2: par2cmdline is not installed")

// ErrRepairFailed means the files could not be repaired.
var ErrRepairFailed = errors.New("par2: repair failed")

var (
	magic       = []byte("PAR2\x00PKT")
	typeMain    = []byte("PAR 2.0\x00Main\x00\x00\x00\x00")
	typeFile    = []byte("PAR 2.0\x00FileDesc")
	headerSize  = 64
	maxPacket   = 1 << 26 // sanity limit for a single packet
	recoveryVol = regexp.MustCompile(`(?i)\.vol(\d+)\+(\d+)\.par2$`)
)

// File describes one protected file.
type File struct {
	Name    string
	Size    int64
	MD5     [16]byte
	MD5_16k [16]byte // MD5 of the first 16 KiB
}

// Index is the content of a PAR2 index file.
type Index struct {
	SliceSize int64
	Files     []File
}

// Parse reads the main and file description packets. Other packets
// (checksums, recovery slices, creator) are skipped.
func Parse(r io.Reader) (*Index, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	idx := &Index{}
	seen := map[[16]byte]bool{}
	header := make([]byte, headerSize)
	for {
		if _, err := io.ReadFull(br, header); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				break // truncated tail; keep what we have
			}
			return nil, err
		}
		if !bytes.Equal(header[:8], magic) {
			return nil, errors.New("par2: invalid packet header")
		}
		length := binary.LittleEndian.Uint64(header[8:16])
		if length < uint64(headerSize) || length > uint64(maxPacket) || length%4 != 0 {
			return nil, fmt.Errorf("par2: invalid packet length %d", length)
		}
		body := make([]byte, length-uint64(headerSize))
		if _, err := io.ReadFull(br, body); err != nil {
			break
		}
		typ := header[48:64]
		switch {
		case bytes.Equal(typ, typeMain) && len(body) >= 12:
			idx.SliceSize = int64(binary.LittleEndian.Uint64(body[:8]))
		case bytes.Equal(typ, typeFile) && len(body) >= 56:
			var id [16]byte
			copy(id[:], body[:16])
			if seen[id] {
				continue // index files repeat packets
			}
			seen[id] = true
			f := File{Size: int64(binary.LittleEndian.Uint64(body[48:56]))}
			copy(f.MD5[:], body[16:32])
			copy(f.MD5_16k[:], body[32:48])
			f.Name = strings.TrimRight(string(body[56:]), "\x00")
			idx.Files = append(idx.Files, f)
		}
	}
	if idx.SliceSize == 0 {
		return nil, errors.New("par2: no main packet")
	}
	return idx, nil
}

// ParseFile parses the index file at path.
func ParseFile(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// RecoveryBlocks returns the number of recovery blocks in a volume named
// like "x.vol07+08.par2" (8), or 0 for other names.
func RecoveryBlocks(name string) int {
	m := recoveryVol.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

// BlocksNeeded estimates the recovery blocks needed to repair missing data:
// every slice touched by a gap is lost. gaps is the number of separate
// missing ranges (each may straddle a slice boundary).
func (idx *Index) BlocksNeeded(missingBytes int64, gaps int) int {
	if missingBytes <= 0 {
		return 0
	}
	return int((missingBytes+idx.SliceSize-1)/idx.SliceSize) + gaps
}

// Deobfuscate renames files in dir whose first 16 KiB match a file of the
// index but whose name differs. It returns the new names.
func (idx *Index) Deobfuscate(dir string) ([]string, error) {
	byHash := map[[16]byte]File{}
	for _, f := range idx.Files {
		byHash[f.MD5_16k] = f
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var renamed []string
	for _, e := range entries {
		if e.IsDir() || strings.EqualFold(filepath.Ext(e.Name()), ".par2") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		sum, err := md5First16k(path)
		if err != nil {
			return renamed, err
		}
		f, ok := byHash[sum]
		target := filepath.Base(filepath.FromSlash(f.Name))
		if !ok || target == e.Name() || target == "." || target == "" {
			continue
		}
		dst := filepath.Join(dir, target)
		if _, err := os.Stat(dst); err == nil {
			continue // never overwrite
		}
		if err := os.Rename(path, dst); err != nil {
			return renamed, err
		}
		renamed = append(renamed, target)
	}
	return renamed, nil
}

func md5First16k(path string) ([16]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [16]byte{}, err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.CopyN(h, f, 16<<10); err != nil && !errors.Is(err, io.EOF) {
		return [16]byte{}, err
	}
	var sum [16]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// Tool returns the path of par2cmdline.
func Tool() (string, error) {
	for _, name := range []string{"par2", "par2cmdline"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", ErrNoTool
}

// Repair verifies and, if needed, repairs the files described by the index
// file using the recovery volumes next to it.
func Repair(ctx context.Context, indexPath string) error {
	tool, err := Tool()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, tool, "r", "-q", "-q", filepath.Base(indexPath))
	cmd.Dir = filepath.Dir(indexPath)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("%w: %s", ErrRepairFailed, lastLines(string(out), 3))
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
