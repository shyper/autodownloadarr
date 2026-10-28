package par2

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// create writes two files and PAR2 data with 10% redundancy in 3 volumes.
func create(t *testing.T) (dir string, a, b []byte) {
	t.Helper()
	tool, err := Tool()
	if err != nil {
		t.Skip("par2cmdline not installed")
	}
	dir = t.TempDir()
	r := rand.New(rand.NewPCG(1, 2))
	a, b = make([]byte, 300_000), make([]byte, 120_000)
	for i := range a {
		a[i] = byte(r.Uint32())
	}
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	_ = os.WriteFile(filepath.Join(dir, "movie.part1.rar"), a, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "movie.part2.rar"), b, 0o644)
	cmd := exec.Command(tool, "create", "-q", "-q", "-s4000", "-r10", "-n3", "movie.par2", "movie.part1.rar", "movie.part2.rar")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("par2 create: %v\n%s", err, out)
	}
	return dir, a, b
}

func TestParseIndex(t *testing.T) {
	dir, a, _ := create(t)
	idx, err := ParseFile(filepath.Join(dir, "movie.par2"))
	if err != nil {
		t.Fatal(err)
	}
	if idx.SliceSize != 4000 || len(idx.Files) != 2 {
		t.Fatalf("index = %+v", idx)
	}
	var found bool
	for _, f := range idx.Files {
		if f.Name == "movie.part1.rar" && f.Size == int64(len(a)) {
			found = true
		}
	}
	if !found {
		t.Errorf("files = %+v", idx.Files)
	}
	if idx.BlocksNeeded(0, 0) != 0 || idx.BlocksNeeded(5000, 1) != 3 {
		t.Errorf("BlocksNeeded = %d, %d", idx.BlocksNeeded(0, 0), idx.BlocksNeeded(5000, 1))
	}
	if _, err := Parse(bytes.NewReader([]byte("not par2 at all, sorry"))); err == nil {
		t.Error("garbage accepted")
	}
}

func TestRecoveryBlocks(t *testing.T) {
	for name, want := range map[string]int{"x.vol000+032.par2": 32, "X.VOL07+08.PAR2": 8, "x.par2": 0, "x.rar": 0} {
		if got := RecoveryBlocks(name); got != want {
			t.Errorf("RecoveryBlocks(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestRepair(t *testing.T) {
	dir, a, _ := create(t)
	damaged := bytes.Clone(a)
	for i := 100_000; i < 110_000; i++ {
		damaged[i] ^= 0xff
	}
	_ = os.WriteFile(filepath.Join(dir, "movie.part1.rar"), damaged, 0o644)
	if err := Repair(context.Background(), filepath.Join(dir, "movie.par2")); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "movie.part1.rar")); !bytes.Equal(got, a) {
		t.Error("file not repaired")
	}

	// Without enough recovery data the repair fails.
	matches, _ := filepath.Glob(filepath.Join(dir, "movie.vol*.par2"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
	_ = os.WriteFile(filepath.Join(dir, "movie.part1.rar"), damaged, 0o644)
	if err := Repair(context.Background(), filepath.Join(dir, "movie.par2")); !errors.Is(err, ErrRepairFailed) {
		t.Errorf("repair without recovery data: %v", err)
	}
}

func TestDeobfuscate(t *testing.T) {
	dir, a, b := create(t)
	_ = os.Rename(filepath.Join(dir, "movie.part1.rar"), filepath.Join(dir, "a8f3c2e91b"))
	_ = os.Rename(filepath.Join(dir, "movie.part2.rar"), filepath.Join(dir, "77d01e5c.bin"))
	_ = os.WriteFile(filepath.Join(dir, "unrelated.nfo"), []byte("hello"), 0o644)
	idx, _ := ParseFile(filepath.Join(dir, "movie.par2"))

	renamed, err := idx.Deobfuscate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(renamed) != 2 {
		t.Errorf("renamed = %v", renamed)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "movie.part1.rar")); !bytes.Equal(got, a) {
		t.Error("part1 not restored")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "movie.part2.rar")); !bytes.Equal(got, b) {
		t.Error("part2 not restored")
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated.nfo")); err != nil {
		t.Error("unrelated file touched")
	}
}
