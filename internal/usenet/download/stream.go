package download

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ErrDataMissing is returned by streaming reads that hit an article that is
// missing on all servers. The data has to be repaired before extraction.
var ErrDataMissing = errors.New("download: data missing")

// Open implements fs.FS for the job's files. Reads block until the requested
// bytes have been downloaded, so files can be consumed while the download
// is still running. The files do not support seeking.
func (j *Job) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for {
		allNamed := true
		for _, st := range j.files {
			if st.name == name && !st.skipped {
				return &stream{job: j, st: st}, nil
			}
			if st.name == "" {
				allNamed = false
			}
		}
		// Unnamed files get their name from their first article; until
		// then the name might still turn up.
		if allNamed || j.ended || j.ctx.Err() != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		j.cond.Wait()
	}
}

// stream reads a file of a running download.
type stream struct {
	job *Job
	st  *fileState
	f   *os.File
	off int64
}

func (s *stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	j, st := s.job, s.st
	j.mu.Lock()
	for {
		if st.f != nil && s.off >= st.size {
			j.mu.Unlock()
			return 0, io.EOF
		}
		if st.avail > s.off {
			break
		}
		if err := s.blocked(); err != nil {
			j.mu.Unlock()
			return 0, err
		}
		j.cond.Wait()
	}
	n := int(min(int64(len(p)), st.avail-s.off))
	path := filepath.Join(j.opts.Dir, st.name)
	j.mu.Unlock()

	if s.f == nil {
		f, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		s.f = f
	}
	n, err := s.f.ReadAt(p[:n], s.off)
	s.off += int64(n)
	if errors.Is(err, io.EOF) && n > 0 {
		err = nil
	}
	return n, err
}

// blocked returns an error if the next byte will never arrive. The caller
// holds the job lock.
func (s *stream) blocked() error {
	st := s.st
	if st.gap {
		return fmt.Errorf("%w: %s: articles do not line up", ErrDataMissing, st.name)
	}
	if st.prefix < len(st.seg) && st.seg[st.prefix].failed {
		return fmt.Errorf("%w: %s: article %d unavailable", ErrDataMissing, st.name, st.prefix+1)
	}
	if s.job.ended || s.job.ctx.Err() != nil {
		if err := s.job.ctx.Err(); err != nil && !s.job.ended {
			return err
		}
		return fmt.Errorf("%w: %s: download ended at byte %d", ErrDataMissing, st.name, st.avail)
	}
	return nil
}

func (s *stream) Stat() (fs.FileInfo, error) {
	s.job.mu.Lock()
	defer s.job.mu.Unlock()
	return fileInfo{name: s.st.name, size: s.st.size}, nil
}

func (s *stream) Close() error {
	if s.f != nil {
		return s.f.Close()
	}
	return nil
}

type fileInfo struct {
	name string
	size int64
}

func (fi fileInfo) Name() string       { return fi.name }
func (fi fileInfo) Size() int64        { return fi.size }
func (fi fileInfo) Mode() fs.FileMode  { return 0o444 }
func (fi fileInfo) ModTime() time.Time { return time.Time{} }
func (fi fileInfo) IsDir() bool        { return false }
func (fi fileInfo) Sys() any           { return nil }
