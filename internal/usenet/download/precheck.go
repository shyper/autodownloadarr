package download

import (
	"context"
	"errors"
	"sync"

	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
)

// Precheck asks the servers (STAT, no download) whether a sample of the
// articles exists. Releases with many missing articles can be skipped before
// downloading anything. Recovery volumes are not checked.
func Precheck(ctx context.Context, pool *nntp.Pool, n *nzb.NZB, sample int) (checked, missing int, err error) {
	var all []nzb.Segment
	for _, f := range n.Files {
		if !IsRecoveryPar2(f.Filename) {
			all = append(all, f.Segments...)
		}
	}
	if len(all) == 0 || sample <= 0 {
		return 0, 0, nil
	}
	// Evenly spread over the release, always including first and last.
	var picks []nzb.Segment
	step := float64(len(all)-1) / float64(max(sample-1, 1))
	seen := map[int]bool{}
	for i := 0; i < sample && i < len(all); i++ {
		k := int(float64(i)*step + 0.5)
		if !seen[k] {
			seen[k] = true
			picks = append(picks, all[k])
		}
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)
	sem := make(chan struct{}, 4)
	for _, seg := range picks {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			err := pool.Stat(ctx, seg.MessageID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				checked++
			case errors.Is(err, nntp.ErrNoSuchArticle):
				checked++
				missing++
			case firstErr == nil:
				firstErr = err
			}
		})
	}
	wg.Wait()
	if checked == 0 && firstErr != nil {
		return 0, 0, firstErr
	}
	return checked, missing, nil
}
