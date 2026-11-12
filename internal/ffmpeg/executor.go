package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"Transcoder/pkg/logger"
)

type Executor struct {
	log *logger.Logger
}

func NewExecutor() *Executor {
	return &Executor{
		log: logger.NewLogger("ffmpeg"),
	}
}

func (e *Executor) Run(ctx context.Context, cmd Command) ([]byte, error) {
	if e == nil || e.log == nil {
		return nil, errors.New("ffmpeg executor is not initialized")
	}
	if cmd == nil {
		return nil, errors.New("command is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	args := cmd.Args()
	e.log.Infof("starting %s", cmd.Tool())
	e.log.Debugf("%s args: %v", cmd.Tool(), args)

	startedAt := time.Now()
	output, err := exec.CommandContext(ctx, cmd.Tool(), args...).CombinedOutput()
	if err != nil {
		var resultErr error
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			resultErr = fmt.Errorf("%w: %s", ErrTimeout, cmd.Tool())
		case ctx.Err() != nil:
			resultErr = fmt.Errorf("%w: %s", ErrCanceled, cmd.Tool())
		case errors.Is(err, exec.ErrNotFound):
			resultErr = fmt.Errorf("%w: %s", ErrToolNotFound, cmd.Tool())
		default:
			resultErr = fmt.Errorf("%w: %s: %w", ErrCommandFailed, cmd.Tool(), err)
		}

		e.log.Errorf(
			"%s failed after %s: %v: %s",
			cmd.Tool(),
			time.Since(startedAt).Round(time.Millisecond),
			resultErr,
			strings.TrimSpace(string(output)),
		)
		return output, resultErr
	}

	e.log.Infof("%s completed in %s", cmd.Tool(), time.Since(startedAt).Round(time.Millisecond))
	return output, nil
}
