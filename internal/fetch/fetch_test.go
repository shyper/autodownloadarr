package fetch

import (
	"testing"

	"github.com/shyper/autodownloadarr/internal/usenet/download"
	"github.com/shyper/autodownloadarr/internal/usenet/nzb"
	"github.com/shyper/autodownloadarr/internal/usenet/par2"
)

func TestChooseRecovery(t *testing.T) {
	n := &nzb.NZB{Files: []nzb.File{
		{Filename: "x.vol00+01.par2"}, {Filename: "x.vol01+02.par2"}, {Filename: "x.vol03+04.par2"}, {Filename: "x.vol07+08.par2"},
	}}
	var skipped []download.FileResult
	for i := range n.Files {
		skipped = append(skipped, download.FileResult{Index: i, Skipped: true})
	}
	got, total := chooseRecovery(n, skipped, 3)
	if total != 15 || len(got.Files) != 1 || got.Files[0].Filename != "x.vol03+04.par2" {
		t.Errorf("needed 3: %v (total %d)", got.Files, total)
	}
	got, _ = chooseRecovery(n, skipped, 11)
	sum := 0
	for _, f := range got.Files {
		sum += par2.RecoveryBlocks(f.Filename)
	}
	if sum < 11 {
		t.Errorf("needed 11, got %d blocks", sum)
	}
}

func TestHelpers(t *testing.T) {
	if !IsSample("Sample/film.mkv") || !IsSample("film.sample.mkv") || IsSample("samples.of.life.mkv") {
		t.Error("IsSample")
	}
	if !IsVideo("a.MKV") || IsVideo("a.nfo") {
		t.Error("IsVideo")
	}
	if !IsReleaseProblem(KindSpam) || IsReleaseProblem(KindDiskError) {
		t.Error("IsReleaseProblem")
	}
}
