package fetch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/shyper/autodownloadarr/internal/usenet/nntp"
	"github.com/shyper/autodownloadarr/internal/usenet/nntp/nntptest"
)

func makeProbeFixture(t *testing.T, audioOnly bool) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fixture.mkv")
	args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=160x90:r=1", "-t", "1", "-c:v", "ffv1", "-y", file}
	if audioOnly {
		args = []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-t", "1", "-c:a", "pcm_s16le", "-y", file}
	}
	if out, err := exec.CommandContext(t.Context(), "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture: %v %s", err, out)
	}
	return file
}

func TestValidateVideo(t *testing.T) {
	good := makeProbeFixture(t, false)
	if err := ValidateVideo(t.Context(), good); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"empty", "text", "audio", "truncated"} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "bad.mp4")
			data := []byte{}
			switch name {
			case "text":
				data = []byte("this is not a video")
			case "audio":
				file = makeProbeFixture(t, true)
			case "truncated":
				raw, err := os.ReadFile(good)
				if err != nil {
					t.Fatal(err)
				}
				data = raw[:16]
			}
			if name != "audio" {
				if err := os.WriteFile(file, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			err := ValidateVideo(t.Context(), file)
			var failure *Error
			if !errors.As(err, &failure) || failure.Kind != KindInvalidVideo || !IsReleaseProblem(failure.Kind) {
				t.Fatalf("invalid video: %v", err)
			}
		})
	}
	t.Run("missing probe", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		err := ValidateVideo(t.Context(), good)
		var failure *Error
		if !errors.As(err, &failure) || failure.Kind != KindVideoProbe || IsReleaseProblem(failure.Kind) {
			t.Fatalf("missing probe: %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := ValidateVideo(ctx, good)
		var failure *Error
		if !errors.As(err, &failure) || failure.Kind != KindCancelled {
			t.Fatalf("cancelled: %v", err)
		}
	})
}

// Exercise real probing through the complete fetch path, including cleanup.
func TestFetchValidatesCompletedVideo(t *testing.T) {
	for _, valid := range []bool{true, false} {
		name := "invalid"
		if valid {
			name = "valid"
		}
		t.Run(name, func(t *testing.T) {
			source := makeProbeFixture(t, false)
			if !valid {
				if err := os.WriteFile(source, []byte("renamed non-video content"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			srv := nntptest.New(t)
			nzb := postObfuscated(t, srv, []string{source})
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			pool, err := nntp.NewPool([]nntp.Config{srv.Config(2)}, log)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			root := t.TempDir()
			folder := filepath.Join(root, "library")
			work := filepath.Join(root, WorkDirName, "incomplete", "test")
			f := &Fetcher{Log: log}
			result, err := f.Fetch(t.Context(), Request{Pool: pool, NZB: nzb, Root: root, WorkDir: work, Folder: folder, VideoName: "Film"})
			if valid {
				if err != nil || len(result.Videos) != 1 {
					t.Fatalf("valid result: %+v %v", result, err)
				}
			} else {
				var failure *Error
				if !errors.As(err, &failure) || failure.Kind != KindInvalidVideo {
					t.Fatalf("invalid result: %+v %v", result, err)
				}
				files, _ := filepath.Glob(filepath.Join(folder, "*"))
				if len(files) != 0 {
					t.Fatalf("invalid files left: %v", files)
				}
			}
			if _, err := os.Stat(work); !os.IsNotExist(err) {
				t.Errorf("work directory not removed: %v", err)
			}
		})
	}
}
