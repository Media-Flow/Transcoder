package transcoder

import (
	"Transcoder/internal/ffmpeg"
	"Transcoder/internal/media"
	"Transcoder/internal/storage"
	"Transcoder/pkg/logger"
	"context"
	"errors"
	"fmt"
	"path"
	"time"
)

var (
	ErrNotInitialized = errors.New("transcoder is not initialized")
	ErrEventRequired  = errors.New("transcode event is required")
	ErrInputRequired  = errors.New("source key is required")
	ErrNoVideoStream  = errors.New("source has no video stream")
)

type Transcoder struct {
	log            *logger.Logger
	mediaService   *media.Service
	executor       *ffmpeg.Executor
	storageService *storage.StorageService
}

func NewTranscoder(
	executor *ffmpeg.Executor,
	mediaService *media.Service,
	storageService *storage.StorageService,
) *Transcoder {
	return &Transcoder{
		log:            logger.NewLogger("Transcoder"),
		mediaService:   mediaService,
		executor:       executor,
		storageService: storageService,
	}
}

func (t *Transcoder) Transcode(ctx context.Context, event *media.TranscodeEvent, mediaKey string) (err error) {
	if t == nil || t.log == nil || t.executor == nil ||
		t.mediaService == nil || t.storageService == nil {
		return ErrNotInitialized
	}
	if event == nil {
		return ErrEventRequired
	}
	if mediaKey == "" {
		return ErrInputRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}

	defer func() {
		if err == nil {
			return
		}

		failCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			10*time.Second,
		)
		defer cancel()

		if failErr := t.mediaService.FailEvent(failCtx, event.ID, err.Error()); failErr != nil {
			t.log.Errorf("mark event=%s failed: %v", event.EventID, failErr)
		}
	}()
	t.log.Infof("transcoding media=%s", mediaKey)

	inputURL, err := t.storageService.GetPresignedUrl(ctx, mediaKey)
	if err != nil {
		return fmt.Errorf("presign source: %w", err)
	}

	probeOutput, err := t.executor.Run(ctx, ffmpeg.NewProbeCommand(inputURL))
	if err != nil {
		return fmt.Errorf("probe source: %w", err)
	}

	probeResult, err := ffmpeg.ParseProbeResult(probeOutput)
	if err != nil {
		return fmt.Errorf("parse probe result: %w", err)
	}

	if probeResult.Width <= 0 || probeResult.Height <= 0 {
		return ErrNoVideoStream
	}

	planned := planVariants(probeResult)

	hlsCommand, err := ffmpeg.NewHLSCommand(event.MediaID.String(), inputURL, planned, probeResult.HasAudio, probeResult.FrameRate)
	if err != nil {
		return fmt.Errorf("create HLS command: %w", err)
	}

	defer func() {
		if cleanupErr := hlsCommand.Cleanup(); cleanupErr != nil {
			t.log.Warnf("clean HLS workspace: %v", cleanupErr)
		}
	}()

	_, err = t.executor.Run(ctx, hlsCommand)
	if err != nil {
		return fmt.Errorf("run HLS: %w", err)
	}

	if err := t.storageService.UploadMultipleFiles(
		ctx,
		event.MediaID.String(),
		hlsCommand.OutputDir(),
	); err != nil {
		return fmt.Errorf("upload HLS output: %w", err)
	}

	variants := buildVariants(event.MediaID.String(), planned, probeResult.FrameRate)
	if err := t.mediaService.CompleteEvent(ctx, event.ID, variants); err != nil {
		return fmt.Errorf("complete event: %w", err)
	}

	t.log.Infof("transcode complete media=%s event=%s", event.MediaID, event.EventID)
	return nil
}

func planVariants(probe *ffmpeg.ProbeResult) []ffmpeg.Variant {
	shortSide := min(probe.Width, probe.Height)

	if shortSide >= ffmpeg.Profile1080p.Height {
		return []ffmpeg.Variant{
			ffmpeg.Profile1080p,
			ffmpeg.Profile720p,
		}
	}

	return []ffmpeg.Variant{
		ffmpeg.Profile720p,
	}
}

func buildVariants(
	mediaID string,
	planned []ffmpeg.Variant,
	frameRate float64,
) []media.TranscodeVariant {
	codec := "h264"
	container := "hls"

	result := make([]media.TranscodeVariant, 0, len(planned))

	for _, variant := range planned {
		width := variant.Width
		height := variant.Height
		bitrate := variant.VideoBitrate
		playlistKey := storage.HLSKey(mediaID, path.Join(variant.Name, "index.m3u8"))

		result = append(result, media.TranscodeVariant{
			VariantKey:  variant.Name,
			Width:       &width,
			Height:      &height,
			Bitrate:     &bitrate,
			Codec:       &codec,
			Container:   &container,
			FrameRate:   &frameRate,
			PlaylistKey: &playlistKey,
		})
	}

	return result
}
