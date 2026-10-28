package fetch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/par2"
	"github.com/shyper/autodownloadarr/internal/usenet/unpack"
)

// repairAndExtract is the fallback when extraction could not follow the
// download: it restores obfuscated names, repairs missing data with PAR2
// (downloading only as many recovery volumes as needed) and extracts the
// archives from disk. It returns the extracted files and, on failure, the
// kind of problem.
func (f *Fetcher) repairAndExtract(ctx context.Context, req Request, n *nzb.NZB, res download.Result,
	src *passwordSource) ([]string, string, string, error) {
	pool, work, folder := req.Pool, req.WorkDir, req.Folder
	usedPassword := ""
	index, idx := findIndex(work)
	if idx != nil {
		if renamed, err := idx.Deobfuscate(work); err != nil {
			return nil, "", KindDiskError, err
		} else if len(renamed) > 0 {
			f.Log.Info("restored obfuscated file names", "files", renamed)
		}
	}

	if !res.Complete() {
		if idx == nil {
			return nil, "", KindIncomplete, fmt.Errorf("%d articles missing and no PAR2 data to repair them", res.Progress.FailedSegments)
		}
		var missing int64
		gaps := 0
		var recovery []download.FileResult
		for _, f := range res.Files {
			if f.Skipped {
				recovery = append(recovery, f)
				continue
			}
			missing += f.MissingBytes
			gaps += f.Gaps
		}
		needed := idx.BlocksNeeded(missing, gaps)
		chosen, available := chooseRecovery(n, recovery, needed)
		if available < needed {
			return nil, "", KindIncomplete, fmt.Errorf("repair needs %d recovery blocks, the release has %d", needed, available)
		}
		if len(chosen.Files) > 0 {
			f.Log.Info("downloading recovery volumes", "blocks_needed", needed, "volumes", len(chosen.Files))
			if _, err := download.Run(ctx, chosen, download.Options{Pool: pool, Dir: work, IncludeRecovery: true, Limiter: req.Limiter, Log: f.Log}); err != nil {
				if ctx.Err() != nil {
					return nil, "", KindCancelled, context.Cause(ctx)
				}
				return nil, "", KindDownloadError, err
			}
		}
		if err := par2.Repair(ctx, index); err != nil {
			if ctx.Err() != nil {
				return nil, "", KindCancelled, context.Cause(ctx)
			}
			return nil, "", KindIncomplete, err
		}
		f.Log.Info("release repaired", "index", filepath.Base(index))
	}

	src.scanDir(work)

	var extracted []string
	rars, err := unpack.FindRARArchives(work)
	if err != nil {
		return nil, "", KindDiskError, err
	}
	for _, path := range rars {
		files, err := withPasswords(src, &usedPassword, func(pw string) ([]string, error) {
			r, err := unpack.ExtractRARFS(ctx, os.DirFS(work), filepath.Base(path), folder, unpack.Options{Password: pw, Include: wanted})
			return r.Files, err
		})
		for _, f := range files {
			extracted = append(extracted, filepath.Join(folder, f))
		}
		if err != nil {
			return extracted, "", extractKind(err), err
		}
	}

	sevens, err := unpack.Find7zArchives(work)
	if err != nil {
		return extracted, "", KindDiskError, err
	}
	for _, path := range sevens {
		tmp := filepath.Join(work, "_extract")
		files, err := withPasswords(src, &usedPassword, func(pw string) ([]string, error) {
			_ = os.RemoveAll(tmp)
			r, err := unpack.Extract7z(ctx, path, tmp, pw)
			return r.Files, err
		})
		if err != nil {
			return extracted, "", extractKind(err), err
		}
		moved, err := moveVideos(tmp, files, folder)
		extracted = append(extracted, moved...)
		if err != nil {
			return extracted, "", KindDiskError, err
		}
	}

	if len(rars) == 0 && len(sevens) == 0 {
		// No archive: the video files were posted directly.
		names := []string{}
		entries, _ := os.ReadDir(work)
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
		moved, err := moveVideos(work, names, folder)
		extracted = append(extracted, moved...)
		if err != nil {
			return extracted, "", KindDiskError, err
		}
	}
	return extracted, usedPassword, "", nil
}

// findIndex returns the PAR2 index file of a download (the .par2 file that
// is not a recovery volume) and its parsed content.
func findIndex(dir string) (string, *par2.Index) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}
	var candidates []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".par2") && par2.RecoveryBlocks(e.Name()) == 0 {
			candidates = append(candidates, filepath.Join(dir, e.Name()))
		}
	}
	// Recovery volumes contain the index packets too; use them if needed.
	if len(candidates) == 0 {
		for _, e := range entries {
			if strings.EqualFold(filepath.Ext(e.Name()), ".par2") {
				candidates = append(candidates, filepath.Join(dir, e.Name()))
			}
		}
	}
	for _, c := range candidates {
		if idx, err := par2.ParseFile(c); err == nil {
			return c, idx
		}
	}
	return "", nil
}

// chooseRecovery picks recovery volumes with at least needed blocks,
// preferring small volumes so that little extra data is downloaded. It also
// returns the number of blocks the release offers in total.
func chooseRecovery(n *nzb.NZB, skipped []download.FileResult, needed int) (*nzb.NZB, int) {
	type vol struct {
		file   nzb.File
		blocks int
	}
	var vols []vol
	total := 0
	for _, f := range skipped {
		nf := n.Files[f.Index]
		if b := par2.RecoveryBlocks(nf.Filename); b > 0 {
			vols = append(vols, vol{nf, b})
			total += b
		}
	}
	slices.SortFunc(vols, func(a, b vol) int { return a.blocks - b.blocks })
	out := &nzb.NZB{Meta: n.Meta}
	got := 0
	// Take the smallest volume that covers the rest alone, else the
	// largest ones until enough.
	for got < needed && len(vols) > 0 {
		i := slices.IndexFunc(vols, func(v vol) bool { return got+v.blocks >= needed })
		if i < 0 {
			i = len(vols) - 1
		}
		out.Files = append(out.Files, vols[i].file)
		got += vols[i].blocks
		vols = slices.Delete(vols, i, i+1)
	}
	return out, total
}

// withPasswords runs an extraction without a password and, if one is
// required, with each candidate.
func withPasswords(src *passwordSource, used *string, try func(pw string) ([]string, error)) ([]string, error) {
	files, err := try("")
	if err == nil || !errors.Is(err, unpack.ErrPasswordRequired) {
		return files, err
	}
	for _, pw := range src.all() {
		files, err = try(pw)
		if err == nil {
			*used = pw
		}
		if err == nil || !errors.Is(err, unpack.ErrWrongPassword) {
			return files, err
		}
	}
	return nil, err
}

func extractKind(err error) string {
	if errors.Is(err, unpack.ErrPasswordRequired) || errors.Is(err, unpack.ErrWrongPassword) {
		return KindPassword
	}
	if errors.Is(err, unpack.ErrNo7z) {
		return KindDiskError
	}
	return KindUnpack
}

// moveVideos moves the wanted video files (relative to from) into folder.
func moveVideos(from string, files []string, folder string) ([]string, error) {
	var moved []string
	for _, f := range files {
		if !wanted(f) {
			continue
		}
		dst := filepath.Join(folder, filepath.Base(f))
		if err := os.Rename(filepath.Join(from, f), dst); err != nil {
			return moved, err
		}
		moved = append(moved, dst)
	}
	return moved, nil
}
