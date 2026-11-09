package media

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrTranscodeEventNotFound      = errors.New("transcode event not found")
	ErrTranscodeEventExists        = errors.New("transcode event already exists")
	ErrTranscodeEventNotClaimable  = errors.New("transcode event is not claimable")
	ErrTranscodeEventNotProcessing = errors.New("transcode event is not processing")
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}
