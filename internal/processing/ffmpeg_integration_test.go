package processing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yourusername/streamforge/internal/queue"
)

func TestFFmpegProcessorRealMediaIntegration(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		if os.Getenv("STREAMFORGE_REQUIRE_FFMPEG_TEST") == "true" {
			t.Fatalf("ffmpeg is required for integration testing but is not on PATH: %v", err)
		}
		t.Skip("ffmpeg is not installed")
	}
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		if os.Getenv("STREAMFORGE_REQUIRE_FFMPEG_TEST") == "true" {
			t.Fatalf("ffprobe is required for integration testing but is not on PATH: %v", err)
		}
		t.Skip("ffprobe is not installed")
	}

	root := t.TempDir()
	inputPath := filepath.Join(root, "fixture.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err = (execRunner{}).Run(ctx, ffmpegPath,
		"-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=44100",
		"-t", "2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-movflags", "+faststart",
		inputPath,
	)
	if err != nil {
		t.Fatalf("generate fixture video: %v", err)
	}

	outputRoot := filepath.Join(root, "output")
	processor := NewFFmpegProcessor(ffprobePath, ffmpegPath, outputRoot, nil)
	result, err := processor.ProcessResult(ctx, queue.Job{ID: "integration-job", VideoID: "integration-video", InputKey: inputPath})
	if err != nil {
		t.Fatalf("process real fixture: %v", err)
	}
	if result.Width != 320 || result.Height != 240 || result.DurationSeconds < 1.5 {
		t.Fatalf("unexpected probed media metadata: %+v", result)
	}
	if len(result.Resolutions) != 1 || result.Resolutions[0] != 240 {
		t.Fatalf("unexpected output resolutions: %v", result.Resolutions)
	}

	videoOutput := filepath.Join(outputRoot, "integration-video")
	master, err := os.ReadFile(filepath.Join(videoOutput, "master.m3u8"))
	if err != nil {
		t.Fatalf("read master playlist: %v", err)
	}
	if !strings.Contains(string(master), "240p/index.m3u8") {
		t.Fatalf("master playlist does not reference 240p rendition: %s", master)
	}
	rendition, err := os.ReadFile(filepath.Join(videoOutput, "240p", "index.m3u8"))
	if err != nil {
		t.Fatalf("read rendition playlist: %v", err)
	}
	if !strings.Contains(string(rendition), ".ts") || !strings.Contains(string(rendition), "#EXT-X-ENDLIST") {
		t.Fatalf("rendition playlist is incomplete: %s", rendition)
	}
	segments, err := filepath.Glob(filepath.Join(videoOutput, "240p", "*.ts"))
	if err != nil || len(segments) == 0 {
		t.Fatalf("HLS segment missing: segments=%v error=%v", segments, err)
	}
	thumbnail, err := os.ReadFile(filepath.Join(videoOutput, "thumbnail.jpg"))
	if err != nil {
		t.Fatalf("read thumbnail: %v", err)
	}
	if len(thumbnail) < 4 || thumbnail[0] != 0xff || thumbnail[1] != 0xd8 {
		t.Fatal("thumbnail is not a JPEG image")
	}
}
