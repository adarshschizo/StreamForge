package processing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourusername/streamforge/internal/queue"
)

type fakeRunner struct {
	calls       []string
	probeOutput []byte
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "ffprobe" {
		if r.probeOutput != nil {
			return r.probeOutput, nil
		}
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

func TestProcessRejectsMediaWithoutVideoStream(t *testing.T) {
	runner := &fakeRunner{probeOutput: []byte(`{"format":{"duration":"1"},"streams":[]}`)}
	processor := FFmpegProcessor{FFprobePath: "ffprobe", FFmpegPath: "ffmpeg", OutputRoot: t.TempDir(), Runner: runner}
	err := processor.Process(context.Background(), queue.Job{ID: "job-audio", VideoID: "audio-only", InputKey: "audio.mp3"})
	if err == nil || !strings.Contains(err.Error(), "video stream not found") {
		t.Fatalf("expected video stream validation error, got %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected only ffprobe to run for invalid media, got %v", runner.calls)
	}
}

type fakeMediaStore struct {
	uploaded []string
}

func (s *fakeMediaStore) Download(_ context.Context, _, destination string) error {
	return os.WriteFile(destination, []byte("video fixture"), 0o600)
}

func (s *fakeMediaStore) UploadFile(_ context.Context, key, _, _ string) error {
	s.uploaded = append(s.uploaded, key)
	return nil
}

func TestStorageProcessorRemovesTemporaryFiles(t *testing.T) {
	localRoot := t.TempDir()
	source, destination := &fakeMediaStore{}, &fakeMediaStore{}
	processor := StorageProcessor{
		Source:      source,
		Destination: destination,
		LocalRoot:   localRoot,
		Processor: FFmpegProcessor{
			FFprobePath: "ffprobe",
			FFmpegPath:  "ffmpeg",
			Runner:      &fakeRunner{},
		},
	}
	result, err := processor.ProcessResult(context.Background(), queue.Job{
		ID:       "job-cleanup",
		VideoID:  "video-cleanup",
		InputKey: "uploads/source.mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 1920 {
		t.Fatalf("unexpected processing result: %+v", result)
	}
	if len(destination.uploaded) == 0 {
		t.Fatal("expected generated media to be uploaded")
	}
	if _, err := os.Stat(filepath.Join(localRoot, "video-cleanup")); !os.IsNotExist(err) {
		t.Fatalf("worker temporary directory remains (stat error: %v)", err)
	}
}
