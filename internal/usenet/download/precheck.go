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

// CheckResult is the outcome of a full availability check.
type CheckResult struct {
	Segments, Missing int
	// MissingBytes is the size of the missing articles of the data files,
	// RecoveryBytes what the PAR2 recovery volumes on the servers can
	// restore.
	MissingBytes, RecoveryBytes int64
}

// Repairable reports whether the missing data can be restored from the
// recovery volumes (with some margin: a missing article damages a whole
// PAR2 block).
func (r CheckResult) Repairable() bool {
	return r.MissingBytes == 0 || float64(r.RecoveryBytes) >= float64(r.MissingBytes)*1.2
}

// checkBatch is how many STAT commands go to a server at once.
const checkBatch = 50

// FullCheck asks the servers (STAT, pipelined) for every article of the
// release, using up to workers connections. progress, if set, is called
// with the number of checked articles.
func FullCheck(ctx context.Context, pool *nntp.Pool, n *nzb.NZB, workers int, progress func(done, total int)) (CheckResult, error) {
	type seg struct {
		id       string
		bytes    int64
		recovery bool
	}
	var all []seg
	for _, f := range n.Files {
		rec := IsRecoveryPar2(f.Filename)
		for _, s := range f.Segments {
			all = append(all, seg{id: s.MessageID, bytes: s.Bytes, recovery: rec})
		}
	}
	res := CheckResult{Segments: len(all)}
	if len(all) == 0 {
		return res, nil
	}
	batches := make(chan []seg)
	go func() {
		defer close(batches)
		for i := 0; i < len(all); i += checkBatch {
			select {
			case batches <- all[i:min(i+checkBatch, len(all))]:
			case <-ctx.Done():
				return
			}
		}
	}()

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		done     int
		firstErr error
	)
	for range max(workers, 1) {
		wg.Go(func() {
			for b := range batches {
				ids := make([]string, len(b))
				for i, s := range b {
					ids[i] = s.id
				}
				found, err := pool.StatMany(ctx, ids)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					continue
				}
				for i, s := range b {
					switch {
					case s.recovery && found[i]:
						res.RecoveryBytes += s.bytes
					case !s.recovery && !found[i]:
						res.Missing++
						res.MissingBytes += s.bytes
					case s.recovery:
						res.Missing++
					}
				}
				done += len(b)
				d := done
				mu.Unlock()
				if progress != nil {
					progress(d, len(all))
				}
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if firstErr != nil {
		return res, firstErr // incomplete check: better not to guess
	}
	return res, nil
}
