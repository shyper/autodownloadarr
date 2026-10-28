// Package rar5 writes uncompressed ("store" method) RAR5 archives for tests,
// as no free tool can create RAR files. Format reference:
// https://www.rarlab.com/technote.htm
package rar5

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

// File is an archive entry.
type File struct {
	Name string // '/' separated
	Data []byte
	Dir  bool
}

var signature = []byte("Rar!\x1a\x07\x01\x00")

const (
	headMain = 1
	headFile = 2
	headEnd  = 5

	hflagData      = 0x0002
	hflagSplitPrev = 0x0008
	hflagSplitNext = 0x0010
)

// Write creates base.rar, or with volumeSize > 0 a multi-volume archive
// base.part1.rar, base.part2.rar, … where each volume carries at most
// volumeSize bytes of file data. It returns the volume paths.
func Write(dir, base string, files []File, volumeSize int) ([]string, error) {
	var volumes [][]byte
	multi := volumeSize > 0
	cur := startVolume(0, multi)
	used := 0

	for _, f := range files {
		if f.Dir {
			cur = append(cur, fileHeader(f, nil, 0, false)...)
			continue
		}
		rest := f.Data
		cont := false
		for {
			chunk := rest
			if multi && len(chunk) > volumeSize-used {
				chunk = rest[:max(volumeSize-used, 0)]
			}
			rest = rest[len(chunk):]
			flags := 0
			if cont {
				flags |= hflagSplitPrev
			}
			if len(rest) > 0 {
				flags |= hflagSplitNext
			}
			cur = append(cur, fileHeader(f, chunk, flags, len(rest) == 0)...)
			cur = append(cur, chunk...)
			used += len(chunk)
			if len(rest) == 0 {
				break
			}
			cur = append(cur, endHeader(true)...)
			volumes = append(volumes, cur)
			cur, used, cont = startVolume(len(volumes), multi), 0, true
		}
	}
	cur = append(cur, endHeader(false)...)
	volumes = append(volumes, cur)

	var paths []string
	for i, v := range volumes {
		name := base + ".rar"
		if multi {
			name = fmt.Sprintf("%s.part%d.rar", base, i+1)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, v, 0o644); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

func startVolume(number int, multi bool) []byte {
	var flags []byte
	switch {
	case !multi:
		flags = vint(nil, 0)
	case number == 0:
		flags = vint(nil, 0x0001) // volume
	default:
		flags = vint(vint(nil, 0x0001|0x0002), uint64(number)) // volume + number field
	}
	return append(append([]byte{}, signature...), header(headMain, 0, 0, flags)...)
}

// fileHeader describes chunk, a part of f. Split parts carry the CRC32 of
// their own data; the last part carries the CRC32 of the whole file.
func fileHeader(f File, chunk []byte, splitFlags int, last bool) []byte {
	var body []byte
	if f.Dir {
		body = vint(body, 0x0001) // directory
		body = vint(body, 0)      // unpacked size
		body = vint(body, 0o755)  // attributes
	} else {
		body = vint(body, 0x0004) // CRC32 present
		body = vint(body, uint64(len(f.Data)))
		body = vint(body, 0o644)
		crc := crc32.ChecksumIEEE(chunk)
		if last {
			crc = crc32.ChecksumIEEE(f.Data)
		}
		body = binary.LittleEndian.AppendUint32(body, crc)
	}
	body = vint(body, 0) // compression: version 0, store
	body = vint(body, 1) // host OS: Unix
	body = vint(body, uint64(len(f.Name)))
	body = append(body, f.Name...)

	flags := splitFlags
	if !f.Dir {
		flags |= hflagData
	}
	return header(headFile, flags, len(chunk), body)
}

func endHeader(moreVolumes bool) []byte {
	var flags uint64
	if moreVolumes {
		flags = 0x0001
	}
	return header(headEnd, 0, 0, vint(nil, flags))
}

// header encodes CRC32, size, type, flags, optional data size and body.
func header(typ, flags, dataSize int, body []byte) []byte {
	h := vint(nil, uint64(typ))
	h = vint(h, uint64(flags))
	if flags&hflagData != 0 {
		h = vint(h, uint64(dataSize))
	}
	h = append(h, body...)
	sized := append(vint(nil, uint64(len(h))), h...)
	return append(binary.LittleEndian.AppendUint32(nil, crc32.ChecksumIEEE(sized)), sized...)
}

func vint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
