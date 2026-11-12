package ffmpeg

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Variant struct {
	Name         string
	Width        int
	Height       int
	VideoBitrate int
}

var (
	Profile1080p = Variant{Name: "1080p", Width: 1920, Height: 1080, VideoBitrate: 5000}
	Profile720p  = Variant{Name: "720p", Width: 1280, Height: 720, VideoBitrate: 2800}
)

type HLSCommand struct {
	BaseCommand
	workDir   string
	outputDir string
	variants  []Variant
	hasAudio  bool
	frameRate float64
}

func NewHLSCommand(
	mediaID string,
	input string,
	variants []Variant,
	hasAudio bool,
	frameRate float64,
) (*HLSCommand, error) {
	if mediaID == "" {
		return nil, ErrMediaIDRequired
	}
	if input == "" {
		return nil, ErrInputRequired
	}
	if len(variants) == 0 {
		return nil, ErrNoVariants
	}
	if frameRate <= 0 {
		frameRate = 30
	}

	workDir, err := os.MkdirTemp("", "transcode-"+mediaID+"-*")
	if err != nil {
		return nil, fmt.Errorf("create HLS workspace: %w", err)
	}

	keepWorkspace := false
	defer func() {
		if !keepWorkspace {
			_ = os.RemoveAll(workDir)
		}
	}()

	outputDir := filepath.Join(workDir, "out")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create HLS output directory: %w", err)
	}
	for _, variant := range variants {
		if variant.Name == "" {
			return nil, fmt.Errorf("%w: name is required", ErrInvalidVariant)
		}
		if err := os.MkdirAll(filepath.Join(outputDir, variant.Name), 0o755); err != nil {
			return nil, fmt.Errorf("create variant directory: %w", err)
		}
	}

	keepWorkspace = true
	return &HLSCommand{
		BaseCommand: BaseCommand{
			tool:  ToolFFmpeg,
			input: input,
		},
		workDir:   workDir,
		outputDir: outputDir,
		variants:  append([]Variant(nil), variants...),
		hasAudio:  hasAudio,
		frameRate: frameRate,
	}, nil
}

func (c *HLSCommand) Args() []string {
	const segmentSeconds = 6

	gop := int(math.Round(c.frameRate * segmentSeconds))
	frameRate := strconv.FormatFloat(c.frameRate, 'f', -1, 64)

	var filter strings.Builder
	fmt.Fprintf(&filter, "[0:v:0]split=%d", len(c.variants))
	for i := range c.variants {
		fmt.Fprintf(&filter, "[v%d]", i)
	}
	for i, variant := range c.variants {
		fmt.Fprintf(
			&filter,
			";[v%d]scale=w=%d:h=%d:force_original_aspect_ratio=decrease,"+
				"pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1[v%dout]",
			i,
			variant.Width,
			variant.Height,
			variant.Width,
			variant.Height,
			i,
		)
	}

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-y",
		"-loglevel", "warning",
		"-progress", "pipe:1",
		"-nostats",
		"-i", c.input,
		"-filter_complex", filter.String(),
	}
	for i := range c.variants {
		args = append(args, "-map", fmt.Sprintf("[v%dout]", i))
	}
	if c.hasAudio {
		for range c.variants {
			args = append(args, "-map", "0:a:0")
		}
	}

	for i, variant := range c.variants {
		profile := "main"
		if variant.Height >= 1080 {
			profile = "high"
		}
		args = append(args,
			fmt.Sprintf("-c:v:%d", i), "libx264",
			fmt.Sprintf("-preset:v:%d", i), "veryfast",
			fmt.Sprintf("-profile:v:%d", i), profile,
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", variant.VideoBitrate),
			fmt.Sprintf("-maxrate:v:%d", i), fmt.Sprintf("%dk", variant.VideoBitrate*107/100),
			fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", variant.VideoBitrate*3/2),
		)
	}

	args = append(args,
		"-pix_fmt", "yuv420p",
		"-r", frameRate,
		"-fps_mode", "cfr",
		"-g", strconv.Itoa(gop),
		"-keyint_min", strconv.Itoa(gop),
		"-sc_threshold", "0",
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds),
	)

	if c.hasAudio {
		for i := range c.variants {
			audioIndex := len(c.variants) + i
			args = append(args,
				fmt.Sprintf("-c:a:%d", audioIndex), "aac",
				fmt.Sprintf("-b:a:%d", audioIndex), "128k",
				fmt.Sprintf("-ac:a:%d", audioIndex), "2",
				fmt.Sprintf("-ar:a:%d", audioIndex), "48000",
			)
		}
	}

	streamMap := make([]string, 0, len(c.variants))
	for i, variant := range c.variants {
		if c.hasAudio {
			streamMap = append(streamMap, fmt.Sprintf("v:%d,a:%d,name:%s", i, i, variant.Name))
			continue
		}
		streamMap = append(streamMap, fmt.Sprintf("v:%d,name:%s", i, variant.Name))
	}

	args = append(args,
		"-f", "hls",
		"-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(c.outputDir, "%v", "segment_%05d.ts"),
		"-master_pl_name", "master.m3u8",
		"-var_stream_map", strings.Join(streamMap, " "),
		filepath.Join(c.outputDir, "%v", "index.m3u8"),
	)

	return args
}

func (c *HLSCommand) WorkDir() string {
	return c.workDir
}

func (c *HLSCommand) OutputDir() string {
	return c.outputDir
}

func (c *HLSCommand) Cleanup() error {
	if c == nil {
		return nil
	}
	return os.RemoveAll(c.workDir)
}
