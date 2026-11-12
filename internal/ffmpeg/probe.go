package ffmpeg

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type ProbeCommand struct {
	BaseCommand
}

func NewProbeCommand(input string) *ProbeCommand {
	return &ProbeCommand{
		BaseCommand: BaseCommand{
			tool:  ToolFFprobe,
			input: input,
		},
	}
}

func (c *ProbeCommand) Args() []string {
	return []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		"-i", c.input,
	}
}

type ProbeResult struct {
	Duration  time.Duration
	Width     int
	Height    int
	FrameRate float64
	HasAudio  bool
}

func ParseProbeResult(data []byte) (*ProbeResult, error) {
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType    string `json:"codec_type"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			AvgFrameRate string `json:"avg_frame_rate"`
			RFrameRate   string `json:"r_frame_rate"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse probe output: %w", err)
	}
	if len(raw.Streams) == 0 {
		return nil, ErrNoStreams
	}

	result := &ProbeResult{
		Duration: parseSeconds(raw.Format.Duration),
	}
	for _, stream := range raw.Streams {
		switch stream.CodecType {
		case "video":
			if result.Width > 0 {
				continue
			}
			result.Width = stream.Width
			result.Height = stream.Height
			result.FrameRate = parseRate(stream.AvgFrameRate)
			if result.FrameRate == 0 {
				result.FrameRate = parseRate(stream.RFrameRate)
			}
		case "audio":
			result.HasAudio = true
		}
	}

	return result, nil
}

func parseSeconds(value string) time.Duration {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func parseRate(value string) float64 {
	value = strings.TrimSpace(value)
	numerator, denominator, ok := strings.Cut(value, "/")
	if !ok {
		rate, _ := strconv.ParseFloat(value, 64)
		return rate
	}

	num, err := strconv.ParseFloat(numerator, 64)
	if err != nil {
		return 0
	}
	den, err := strconv.ParseFloat(denominator, 64)
	if err != nil || den == 0 {
		return 0
	}
	return num / den
}
