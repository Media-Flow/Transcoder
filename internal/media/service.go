package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const DefaultTranscodeLease = 2 * time.Hour

var ErrTranscodeServiceNotInitialized = errors.New("transcode service is not initialized")

type TranscodeOutcome int

const (
	TranscodeOutcomeUnknown TranscodeOutcome = iota
	TranscodeOutcomeProcess
	TranscodeOutcomeIgnored
	TranscodeOutcomeDeadLetter
)

type HandleResult struct {
	Event   *TranscodeEvent
	Outcome TranscodeOutcome
}

type TranscodeEventRepository interface {
	FindTranscodeEvent(ctx context.Context, eventID uuid.UUID, mediaID uuid.UUID) (*TranscodeEvent, error)
	CreateEvent(ctx context.Context, event *TranscodeEvent) error
	ClaimForProcessing(ctx context.Context, id uuid.UUID, lease time.Duration) (*TranscodeEvent, error)
	CompleteEvent(ctx context.Context, id uuid.UUID, variants []TranscodeVariant) error
	FailEvent(ctx context.Context, id uuid.UUID, lastError string) error
}

type Service struct {
	repo  TranscodeEventRepository
	lease time.Duration
}

func NewService(repo TranscodeEventRepository, lease time.Duration) *Service {
	if lease <= 0 {
		lease = DefaultTranscodeLease
	}

	return &Service{
		repo:  repo,
		lease: lease,
	}
}

func (s *Service) HandleTranscodeEvent(
	ctx context.Context,
	eventID uuid.UUID,
	mediaID uuid.UUID,
	variants []TranscodeVariant,
) (HandleResult, error) {
	if s == nil || s.repo == nil {
		return HandleResult{}, ErrTranscodeServiceNotInitialized
	}
	if eventID == uuid.Nil {
		return HandleResult{}, errors.New("event ID is required")
	}
	if mediaID == uuid.Nil {
		return HandleResult{}, errors.New("media ID is required")
	}

	event, err := s.repo.FindTranscodeEvent(ctx, eventID, mediaID)
	if errors.Is(err, ErrTranscodeEventNotFound) {
		return s.createAndClaim(ctx, eventID, mediaID, variants)
	}
	if err != nil {
		return HandleResult{}, fmt.Errorf("find transcode event: %w", err)
	}
	return s.handleExistingEvent(ctx, event)
}

func (s *Service) createAndClaim(
	ctx context.Context,
	eventID uuid.UUID,
	mediaID uuid.UUID,
	variants []TranscodeVariant,
) (HandleResult, error) {
	lockedUntil := time.Now().UTC().Add(s.lease)
	event := &TranscodeEvent{
		EventID:     eventID,
		MediaID:     mediaID,
		Status:      TranscodeStatusProcessing,
		Attempts:    1,
		LockedUntil: &lockedUntil,
		Variants:    variants,
	}

	err := s.repo.CreateEvent(ctx, event)
	if err == nil {
		return HandleResult{
			Event:   event,
			Outcome: TranscodeOutcomeProcess,
		}, nil
	}
	if !errors.Is(err, ErrTranscodeEventExists) {
		return HandleResult{}, fmt.Errorf("create transcode event: %w", err)
	}

	event, err = s.repo.FindTranscodeEvent(ctx, eventID, mediaID)
	if err != nil {
		return HandleResult{}, fmt.Errorf("reload transcode event after create conflict: %w", err)
	}

	return s.handleExistingEvent(ctx, event)
}

func (s *Service) handleExistingEvent(
	ctx context.Context,
	event *TranscodeEvent,
) (HandleResult, error) {
	if event == nil {
		return HandleResult{}, errors.New("transcode event is nil")
	}

	switch event.Status {
	case TranscodeStatusComplete:
		return HandleResult{
			Event:   event,
			Outcome: TranscodeOutcomeIgnored,
		}, nil

	case TranscodeStatusFailed, TranscodeStatusProcessing:
		claimed, err := s.repo.ClaimForProcessing(ctx, event.ID, s.lease)
		if err != nil {
			return claimFailureResult(event, err)
		}

		claimed.Variants = event.Variants

		return HandleResult{
			Event:   claimed,
			Outcome: TranscodeOutcomeProcess,
		}, nil

	default:
		return HandleResult{}, fmt.Errorf("unsupported transcode status %q", event.Status)
	}
}

func claimFailureResult(event *TranscodeEvent, err error) (HandleResult, error) {
	switch {
	case errors.Is(err, ErrTranscodeEventLocked):
		return HandleResult{
			Event:   event,
			Outcome: TranscodeOutcomeIgnored,
		}, nil

	case errors.Is(err, ErrTranscodeEventAlreadyComplete):
		return HandleResult{
			Event:   event,
			Outcome: TranscodeOutcomeIgnored,
		}, nil

	case errors.Is(err, ErrTranscodeEventAttemptsExhausted):
		return HandleResult{
			Event:   event,
			Outcome: TranscodeOutcomeDeadLetter,
		}, nil

	default:
		return HandleResult{}, fmt.Errorf("claim transcode event: %w", err)
	}
}

func (s *Service) CompleteEvent(
	ctx context.Context,
	id uuid.UUID,
	variants []TranscodeVariant,
) error {
	if s == nil || s.repo == nil {
		return ErrTranscodeServiceNotInitialized
	}

	return s.repo.CompleteEvent(ctx, id, variants)
}

func (s *Service) FailEvent(
	ctx context.Context,
	id uuid.UUID,
	lastError string,
) error {
	if s == nil || s.repo == nil {
		return ErrTranscodeServiceNotInitialized
	}

	return s.repo.FailEvent(ctx, id, lastError)
}
