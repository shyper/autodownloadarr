// Package unpack extracts downloaded archives.
//
// RAR archives (RAR 1.5–5, multi-volume, encrypted) can be extracted from
// any fs.FS, in particular from a running download.Job, so extraction follows
// the download instead of waiting for it. 7z and ZIP follow later (see
// docs/PLAN.md).
package unpack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// Errors for encrypted archives.
var (
	ErrPasswordRequired = errors.New("unpack: archive is encrypted, password required")
	ErrWrongPassword    = errors.New("unpack: wrong password")
)

// partialSuffix marks files that are still being extracted, so that media
// servers ignore them until they are complete.
const partialSuffix = ".partial"

var (
	rarSignature4 = []byte("Rar!\x1a\x07\x00")
	rarSignature5 = []byte("Rar!\x1a\x07\x01\x00")

	newStyleVolume = regexp.MustCompile(`(?i)\.part(\d+)\.rar$`)
)

// FirstVolumes picks the first volume of every RAR archive from a list of
// file names: "x.part01.rar" (or part1/part001) for new-style names and
// "x.rar" for old-style ones (x.rar, x.r00, …). It works on names only, so it
// can be used before the files exist.
func FirstVolumes(names []string) []string {
	var first []string
	for _, name := range names {
		if !strings.EqualFold(filepath.Ext(name), ".rar") {
			continue
		}
		if m := newStyleVolume.FindStringSubmatch(name); m != nil {
			if n, _ := strconv.Atoi(m[1]); n != 1 {
				continue
			}
		}
		first = append(first, name)
	}
	slices.Sort(first)
	return first
}

// FindRARArchives returns the paths of the first volumes in dir, skipping
// files that do not start with a RAR signature.
func FindRARArchives(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	var paths []string
	for _, name := range FirstVolumes(names) {
		if p := filepath.Join(dir, name); isRAR(p) {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func isRAR(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(rarSignature5))
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	return bytes.HasPrefix(head, rarSignature4) || bytes.HasPrefix(head, rarSignature5)
}

// Result lists what an extraction produced and consumed.
type Result struct {
	// Files are the extracted files (not directories), relative to dest.
	Files []string
	// Volumes are the archive files that were read, as passed to the
	// extractor (paths for ExtractRAR, names for ExtractRARFS).
	Volumes []string
}

// Options control an extraction.
type Options struct {
	Password string
	// Include, if set, selects the files to extract by their name in the
	// archive; others are skipped.
	Include func(name string) bool
}

// ExtractRAR extracts the archive whose first volume is at path into dest.
func ExtractRAR(ctx context.Context, path, dest, password string) (Result, error) {
	dir := filepath.Dir(path)
	res, err := ExtractRARFS(ctx, os.DirFS(dir), filepath.Base(path), dest, Options{Password: password})
	for i, v := range res.Volumes {
		res.Volumes[i] = filepath.Join(dir, filepath.FromSlash(v))
	}
	return res, err
}

// ExtractRARFS extracts the archive whose first volume is firstVolume in
// fsys into dest. With a download.Job as fsys, extraction proceeds as the
// volumes arrive. Entries that would end up outside dest and links are
// skipped; files are written as name.partial and renamed when complete.
func ExtractRARFS(ctx context.Context, fsys fs.FS, firstVolume, dest string, opts Options) (res Result, err error) {
	password := opts.Password
	rarOpts := []rardecode.Option{rardecode.FileSystem(fsys), rardecode.BufferSize(1 << 20)}
	if password != "" {
		rarOpts = append(rarOpts, rardecode.Password(password))
	}
	rc, err := rardecode.OpenReader(firstVolume, rarOpts...)
	if err != nil {
		return Result{}, rarError(err, password)
	}
	defer rc.Close()

	defer func() { res.Volumes = rc.Volumes() }()
	for {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		h, err := rc.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return res, rarError(err, password)
		}
		if h.LinkType != 0 {
			continue // symlinks and hard links could point outside dest
		}
		if !h.IsDir && opts.Include != nil && !opts.Include(h.Name) {
			continue
		}
		target, err := safeJoin(dest, h.Name)
		if err != nil {
			return res, err
		}
		if h.IsDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return res, err
			}
			continue
		}
		if err := extractFile(ctx, rc, target); err != nil {
			return res, rarError(err, password)
		}
		res.Files = append(res.Files, filepath.FromSlash(h.Name))
	}
	return res, nil
}

func extractFile(ctx context.Context, r io.Reader, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	partial := target + partialSuffix
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, ctxReader{ctx: ctx, r: r})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(partial, target)
	}
	if err != nil {
		_ = os.Remove(partial)
	}
	return err
}

// safeJoin resolves an archive entry name below dest.
func safeJoin(dest, name string) (string, error) {
	local := filepath.FromSlash(name)
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("unpack: unsafe path %q in archive", name)
	}
	return filepath.Join(dest, local), nil
}

func rarError(err error, password string) error {
	switch {
	case errors.Is(err, rardecode.ErrArchiveEncrypted), errors.Is(err, rardecode.ErrArchivedFileEncrypted):
		if password == "" {
			return fmt.Errorf("%w: %w", ErrPasswordRequired, err)
		}
		return fmt.Errorf("%w: %w", ErrWrongPassword, err)
	case errors.Is(err, rardecode.ErrBadPassword):
		return fmt.Errorf("%w: %w", ErrWrongPassword, err)
	case errors.Is(err, rardecode.ErrBadFileChecksum) && password != "":
		// RAR3/4 cannot verify a password up front; a checksum error with a
		// password set usually means the password is wrong.
		return fmt.Errorf("%w (or the archive is damaged): %w", ErrWrongPassword, err)
	}
	return fmt.Errorf("unpack: %w", err)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
