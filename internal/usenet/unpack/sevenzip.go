package unpack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ErrNo7z is returned when no 7-Zip binary is installed.
var ErrNo7z = errors.New("unpack: 7-Zip is not installed")

var (
	sevenZipSignature = []byte("7z\xbc\xaf\x27\x1c")
	zipSignature      = []byte("PK\x03\x04")
	splitArchive      = regexp.MustCompile(`(?i)\.(7z|zip)\.(\d{3})$`)
)

// Find7zArchives returns the 7z and ZIP archives in dir (the first part of
// split archives such as x.7z.001). Files without the matching signature
// are ignored.
func Find7zArchives(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if m := splitArchive.FindStringSubmatch(name); m != nil && m[2] != "001" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".7z" && ext != ".zip" && !splitArchive.MatchString(name) {
			continue
		}
		path := filepath.Join(dir, name)
		if hasPrefix(path, sevenZipSignature) || hasPrefix(path, zipSignature) {
			out = append(out, path)
		}
	}
	slices.Sort(out)
	return out, nil
}

func hasPrefix(path string, sig []byte) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(sig))
	n, _ := io.ReadFull(f, head)
	return bytes.Equal(head[:n], sig)
}

// SevenZip returns the path of the 7-Zip binary (7zz, 7z or 7za).
func SevenZip() (string, error) {
	for _, name := range []string{"7zz", "7z", "7za"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", ErrNo7z
}

// Extract7z extracts a 7z or ZIP archive into dest with the 7-Zip command
// line tool and returns the extracted files relative to dest. Entries with
// absolute or parent paths are rejected by 7-Zip itself.
func Extract7z(ctx context.Context, archive, dest, password string) (Result, error) {
	tool, err := SevenZip()
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Result{}, err
	}
	before := listFiles(dest)
	// -p always: without it 7-Zip would prompt for a password and hang.
	cmd := exec.CommandContext(ctx, tool, "x", "-y", "-bd", "-p"+password, "-o"+dest, archive)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}

	var res Result
	for _, f := range listFiles(dest) {
		if !slices.Contains(before, f) {
			res.Files = append(res.Files, f)
		}
	}
	res.Volumes = []string{archive}
	if runErr == nil {
		return res, nil
	}
	msg := out.String()
	switch {
	case strings.Contains(msg, "Wrong password") && password == "":
		return res, fmt.Errorf("%w: %s", ErrPasswordRequired, filepath.Base(archive))
	case strings.Contains(msg, "Wrong password"):
		return res, fmt.Errorf("%w: %s", ErrWrongPassword, filepath.Base(archive))
	}
	return res, fmt.Errorf("unpack: 7-Zip failed: %s", tail(msg))
}

// listFiles returns all regular files below dir, relative to it.
func listFiles(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if rel, err := filepath.Rel(dir, p); err == nil {
				out = append(out, rel)
			}
		}
		return nil
	})
	return out
}

func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, " | ")
}
