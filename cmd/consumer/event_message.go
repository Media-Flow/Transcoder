package consumer

import "github.com/google/uuid"

type EventMessage struct {
	EventId uuid.UUID `json:"eventId"`
	MediaID uuid.UUID `json:"mediaId"`
	Payload struct {
		SourceKey string `json:"sourceKey"`
	} `json:"payload"`
}
