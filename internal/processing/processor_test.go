package processing

import (
	"context"
	"strings"
	"testing"

	"github.com/yourusername/streamforge/internal/queue"
)

type fakeRunner struct {
	calls []string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "ffprobe" {
		return []byte(`{"format":{"duration":"12.5","size":"42"},"streams":[{"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"bit_rate":"1000"}]}`), nil
	}
	return nil, nil
}

func TestProbeAndSupportedResolutions(t *testing.T) {
	runner := &fakeRunner{}
	processor := FFmpegProcessor{FFprobePath: "ffprobe", FFmpegPath: "ffmpeg", OutputRoot: t.TempDir(), Runner: runner}
	metadata, err := processor.Probe(context.Background(), "input.mp4")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := metadata.videoStream()
	if err != nil || stream.Height != 1080 {
		t.Fatalf("unexpected stream: %+v, %v", stream, err)
	}
	got := supportedResolutions(stream.Height)
	if len(got) != 3 || got[0] != 360 || got[2] != 1080 {
		t.Fatalf("unexpected resolutions: %v", got)
	}
}

func TestProcessGeneratesThumbnailAndHLS(t *testing.T) {
	runner := &fakeRunner{}
	processor := FFmpegProcessor{FFprobePath: "ffprobe", FFmpegPath: "ffmpeg", OutputRoot: t.TempDir(), Runner: runner}
	err := processor.Process(context.Background(), queue.Job{ID: "job-1", VideoID: "video-1", InputKey: "input.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 5 {
		t.Fatalf("expected probe, thumbnail, and three HLS commands; got %d: %v", len(runner.calls), runner.calls)
	}
}
