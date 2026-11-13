package consumer

import "github.com/google/uuid"

type EventMessage struct {
	EventId   uuid.UUID `json:"eventId"`
	MediaID   uuid.UUID `json:"mediaId"`
	SourceKey string    `json:"sourceKey"`
}
