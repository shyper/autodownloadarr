package fetch

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
)

// streamVideo copies a directly posted video out of the running download into
// folder, so that a media server can play it before the download finishes.
// target is the file name below folder. On failure the partial copy is
// removed again.
func streamVideo(ctx context.Context, job *download.Job, source, folder, target string,
	onVisible func(string)) (string, error) {
	in, err := job.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()

	path := filepath.Join(folder, target)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	w := &visibleWriter{w: out, after: visibleAfter, notify: func() {
		if onVisible != nil {
			onVisible(path)
		}
	}}
	_, err = io.Copy(w, ctxReader{ctx: ctx, r: in})
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		w.fire() // the file is smaller than the threshold
		return target, nil
	}
	_ = os.Remove(path)
	return "", err
}

// visibleWriter calls notify once after a number of bytes went through.
type visibleWriter struct {
	w        io.Writer
	n        int64
	after    int64
	notify   func()
	notified bool
}

func (w *visibleWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	if err == nil && w.n >= w.after {
		w.fire()
	}
	return n, err
}

func (w *visibleWriter) fire() {
	if w.notified {
		return
	}
	w.notified = true
	if w.notify != nil {
		w.notify()
	}
}

// ctxReader stops a copy when the download context is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
