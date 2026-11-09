package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const (
	KindInvalidVideo = "invalid_video"
	KindVideoProbe   = "video_probe_error"
)

// ValidateVideo checks container metadata, not the complete encoded movie.
func ValidateVideo(ctx context.Context, path string) error {
	if info, err := os.Stat(path); err != nil {
		return fail(KindDiskError, err)
	} else if info.Size() == 0 {
		return fail(KindInvalidVideo, errors.New("empty video file"))
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		return fail(KindVideoProbe, errors.New("ffprobe is required to validate downloaded video"))
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, probe, "-v", "error", "-probesize", "10000000", "-analyzeduration", "10000000", "-protocol_whitelist", "file,pipe", "-select_streams", "v:0", "-show_entries", "stream=codec_type,codec_name,width,height", "-of", "json", path)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return fail(KindCancelled, context.Cause(ctx))
	}
	if probeCtx.Err() != nil {
		return fail(KindVideoProbe, errors.New("video probe timed out"))
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fail(KindInvalidVideo, errors.New("ffprobe could not read the video container"))
		}
		return fail(KindVideoProbe, fmt.Errorf("run ffprobe: %w", err))
	}
	var res struct {
		Streams []struct {
			CodecType     string `json:"codec_type"`
			CodecName     string `json:"codec_name"`
			Width, Height int
		}
	}
	if err := json.Unmarshal(output, &res); err != nil {
		return fail(KindVideoProbe, fmt.Errorf("decode ffprobe result: %w", err))
	}
	for _, stream := range res.Streams {
		if stream.CodecType == "video" && stream.CodecName != "" && stream.CodecName != "unknown" && stream.Width > 0 && stream.Height > 0 {
			return nil
		}
	}
	return fail(KindInvalidVideo, errors.New("file contains no readable video stream with known codec and valid dimensions"))
}
