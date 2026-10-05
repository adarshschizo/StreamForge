package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yourusername/streamforge/internal/queue"
)

type Processor interface {
	Process(context.Context, queue.Job) error
}

type Result struct {
	DurationSeconds float64
	Width           int
	Height          int
	Resolutions     []int
}

type ResultProcessor interface {
	ProcessResult(context.Context, queue.Job) (Result, error)
}
type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return output.Bytes(), fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(output.String()))
	}
	return output.Bytes(), nil
}

type ProbeMetadata struct {
	Format struct {
		Duration string `json:"duration"`
		Size     string `json:"size"`
	} `json:"format"`
	Streams []VideoStream `json:"streams"`
}

type VideoStream struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	BitRate   string `json:"bit_rate"`
}

type FFmpegProcessor struct {
	FFprobePath string
	FFmpegPath  string
	OutputRoot  string
	Runner      CommandRunner
	Logger      *slog.Logger
}

type MediaStore interface {
	Download(context.Context, string, string) error
	UploadFile(context.Context, string, string, string) error
}

type StorageProcessor struct {
	Source      MediaStore
	Destination MediaStore
	LocalRoot   string
	Processor   FFmpegProcessor
	Logger      *slog.Logger
}

func (p StorageProcessor) Process(ctx context.Context, job queue.Job) error {
	_, err := p.ProcessResult(ctx, job)
	return err
}

func (p StorageProcessor) ProcessResult(ctx context.Context, job queue.Job) (Result, error) {
	if p.Source == nil || p.Destination == nil {
		return Result{}, errors.New("media storage is not configured")
	}
	workDir := filepath.Join(p.LocalRoot, job.VideoID)
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		return Result{}, fmt.Errorf("create worker directory: %w", err)
	}
	inputPath := filepath.Join(workDir, "original")
	if err := p.Source.Download(ctx, job.InputKey, inputPath); err != nil {
		return Result{}, fmt.Errorf("download original: %w", err)
	}
	job.InputKey = inputPath
	p.Processor.OutputRoot = workDir
	result, err := p.Processor.ProcessResult(ctx, job)
	if err != nil {
		return Result{}, err
	}
	outputRoot := filepath.Join(workDir, job.VideoID)
	if err := filepath.Walk(outputRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(outputRoot, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(filepath.Join("videos", job.VideoID, "hls", relative))
		contentType := "application/octet-stream"
		if strings.HasSuffix(path, ".m3u8") {
			contentType = "application/vnd.apple.mpegurl"
		} else if strings.HasSuffix(path, ".jpg") {
			contentType = "image/jpeg"
		} else if strings.HasSuffix(path, ".ts") {
			contentType = "video/mp2t"
		}
		return p.Destination.UploadFile(ctx, key, path, contentType)
	}); err != nil {
		return Result{}, err
	}
	return result, nil
}

func NewFFmpegProcessor(ffprobePath, ffmpegPath, outputRoot string, logger *slog.Logger) FFmpegProcessor {
	return FFmpegProcessor{
		FFprobePath: ffprobePath,
		FFmpegPath:  ffmpegPath,
		OutputRoot:  outputRoot,
		Runner:      execRunner{},
		Logger:      logger,
	}
}

func (p FFmpegProcessor) Process(ctx context.Context, job queue.Job) error {
	_, err := p.ProcessResult(ctx, job)
	return err
}

func (p FFmpegProcessor) ProcessResult(ctx context.Context, job queue.Job) (Result, error) {
	if err := job.Validate(); err != nil {
		return Result{}, err
	}
	if p.Runner == nil || p.FFprobePath == "" || p.FFmpegPath == "" {
		return Result{}, errors.New("media processor is not configured")
	}
	metadata, err := p.Probe(ctx, job.InputKey)
	if err != nil {
		return Result{}, fmt.Errorf("probe video: %w", err)
	}
	videoStream, err := metadata.videoStream()
	if err != nil {
		return Result{}, err
	}
	outputDir := filepath.Join(p.OutputRoot, job.VideoID)
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return Result{}, fmt.Errorf("create processing output directory: %w", err)
	}
	if err := p.generateThumbnail(ctx, job.InputKey, outputDir); err != nil {
		return Result{}, fmt.Errorf("generate thumbnail: %w", err)
	}
	resolutions := supportedResolutions(videoStream.Height)
	for _, resolution := range resolutions {
		if err := p.generateHLS(ctx, job.InputKey, outputDir, resolution); err != nil {
			return Result{}, fmt.Errorf("generate %dp HLS: %w", resolution, err)
		}
	}
	if err := writeMasterPlaylist(outputDir, resolutions); err != nil {
		return Result{}, fmt.Errorf("write master playlist: %w", err)
	}
	if p.Logger != nil {
		p.Logger.Info("video processed", "job_id", job.ID, "video_id", job.VideoID, "duration", metadata.Format.Duration, "height", videoStream.Height)
	}
	duration, _ := strconv.ParseFloat(metadata.Format.Duration, 64)
	return Result{DurationSeconds: duration, Width: videoStream.Width, Height: videoStream.Height, Resolutions: resolutions}, nil
}

func writeMasterPlaylist(outputDir string, resolutions []int) error {
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, resolution := range resolutions {
		bandwidth := resolution * 1800
		playlist.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n%dp/index.m3u8\n", bandwidth, resolution*16/9, resolution, resolution))
	}
	return os.WriteFile(filepath.Join(outputDir, "master.m3u8"), []byte(playlist.String()), 0o640)
}

func (p FFmpegProcessor) Probe(ctx context.Context, input string) (ProbeMetadata, error) {
	output, err := p.Runner.Run(ctx, p.FFprobePath, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", input)
	if err != nil {
		return ProbeMetadata{}, err
	}
	var metadata ProbeMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return ProbeMetadata{}, fmt.Errorf("decode ffprobe output: %w", err)
	}
	return metadata, nil
}

func (p FFmpegProcessor) generateThumbnail(ctx context.Context, input, outputDir string) error {
	output := filepath.Join(outputDir, "thumbnail.jpg")
	_, err := p.Runner.Run(ctx, p.FFmpegPath, "-y", "-ss", "00:00:01", "-i", input, "-frames:v", "1", "-q:v", "2", output)
	return err
}

func (p FFmpegProcessor) generateHLS(ctx context.Context, input, outputDir string, resolution int) error {
	renditionDir := filepath.Join(outputDir, fmt.Sprintf("%dp", resolution))
	if err := os.MkdirAll(renditionDir, 0o750); err != nil {
		return err
	}
	output := filepath.Join(renditionDir, "index.m3u8")
	scale := fmt.Sprintf("scale=-2:%d", resolution)
	_, err := p.Runner.Run(ctx, p.FFmpegPath, "-y", "-i", input, "-vf", scale, "-c:v", "libx264", "-c:a", "aac", "-hls_time", "6", "-hls_playlist_type", "vod", output)
	return err
}

func (m ProbeMetadata) videoStream() (VideoStream, error) {
	for _, stream := range m.Streams {
		if stream.CodecType == "video" {
			return stream, nil
		}
	}
	return VideoStream{}, errors.New("video stream not found")
}

func supportedResolutions(height int) []int {
	resolutions := make([]int, 0, 3)
	for _, resolution := range []int{360, 720, 1080} {
		if height >= resolution {
			resolutions = append(resolutions, resolution)
		}
	}
	if len(resolutions) == 0 && height > 0 {
		resolutions = append(resolutions, height)
	}
	return resolutions
}

func parseNumber(value string) (int64, error) {
	return strconv.ParseInt(value, 10, 64)
}
