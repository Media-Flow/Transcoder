package ffmpeg

import "errors"

const (
	ToolFFmpeg  = "ffmpeg"
	ToolFFprobe = "ffprobe"
)

var (
	ErrInputRequired     = errors.New("input is required")
	ErrNoVariants        = errors.New("at least one variant is required")
	ErrMediaIDRequired   = errors.New("media ID is required")
	ErrOutputDirRequired = errors.New("output directory is required")
	ErrInvalidVariant    = errors.New("invalid variant")
	ErrToolNotFound      = errors.New("ffmpeg tool not found")
	ErrCommandFailed     = errors.New("command failed")
	ErrTimeout           = errors.New("command timed out")
	ErrCanceled          = errors.New("command canceled")
	ErrNoStreams         = errors.New("probe returned no streams")
)

type Command interface {
	Tool() string
	Args() []string
}

type BaseCommand struct {
	tool  string
	input string
}

func (b BaseCommand) Tool() string {
	return b.tool
}
