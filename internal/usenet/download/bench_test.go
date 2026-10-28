package download_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// BenchmarkDownload measures the whole pipeline (NNTP, yEnc, writing to
// disk) against the local fake server. It shows the client-side ceiling;
// real downloads are limited by the provider and the line.
func BenchmarkDownload(b *testing.B) {
	srv := nntptest.New(b)
	const size = 64 << 20
	n := post(srv, 750_000, postedFile{"bench.bin", randomBytes(size, 99)})
	pool := newPool(b, srv.Config(16))
	b.SetBytes(size)
	for b.Loop() {
		res, err := download.Run(context.Background(), n, download.Options{Pool: pool, Dir: b.TempDir()})
		if err != nil || !res.Complete() {
			b.Fatalf("%v %+v", err, res.Files)
		}
	}
}
