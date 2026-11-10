package media

import (
	"time"

	"github.com/google/uuid"
)

type TranscodeStatus string

const (
	TranscodeStatusProcessing TranscodeStatus = "PROCESSING"
	TranscodeStatusFailed     TranscodeStatus = "FAIL"
	TranscodeStatusComplete   TranscodeStatus = "COMPLETE"
)

const (
	VariantKey1080p = "1080p"
	VariantKey720p  = "720p"
)

const MaxTranscodeAttempts = 3

func (status TranscodeStatus) Valid() bool {
	switch status {
	case TranscodeStatusProcessing, TranscodeStatusFailed, TranscodeStatusComplete:
		return true
	default:
		return false
	}
}

type TranscodeEvent struct {
	ID          uuid.UUID          `json:"id"`
	EventID     uuid.UUID          `json:"eventId"`
	MediaID     uuid.UUID          `json:"mediaId"`
	Status      TranscodeStatus    `json:"status"`
	Attempts    int                `json:"attempts"`
	LastError   *string            `json:"lastError,omitempty"`
	LockedUntil *time.Time         `json:"lockedUntil,omitempty"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	Variants    []TranscodeVariant `json:"variants,omitempty"`
}

type TranscodeVariant struct {
	ID               uuid.UUID `json:"id"`
	TranscodeEventID uuid.UUID `json:"transcodeEventId"`
	VariantKey       string    `json:"variantKey"`
	Width            *int      `json:"width,omitempty"`
	Height           *int      `json:"height,omitempty"`
	Bitrate          *int      `json:"bitrate,omitempty"`
	Codec            *string   `json:"codec,omitempty"`
	Container        *string   `json:"container,omitempty"`
	FrameRate        *float64  `json:"frameRate,omitempty"`
	PlaylistKey      *string   `json:"playlistKey,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}
