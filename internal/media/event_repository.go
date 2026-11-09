package media

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) CreateEvent(ctx context.Context, event *TranscodeEvent) error {
	if event == nil {
		return errors.New("transcode event is nil")
	}
	if event.EventID == uuid.Nil {
		return errors.New("event ID is required")
	}
	if event.MediaID == uuid.Nil {
		return errors.New("media ID is required")
	}
	if event.Status == "" {
		event.Status = TranscodeStatusProcessing
	}
	if !event.Status.Valid() {
		return fmt.Errorf("invalid transcode event status %q", event.Status)
	}
	if event.Attempts < 0 {
		return errors.New("attempts cannot be negative")
	}

	if len(event.Variants) == 0 {
		return errors.New("at least one variant is required")
	}
	if event.Attempts == 0 {
		event.Attempts = 1
	}

	seen := make(map[string]struct{}, len(event.Variants))
	for i := range event.Variants {
		v := &event.Variants[i]
		if strings.TrimSpace(v.VariantKey) == "" {
			return fmt.Errorf("variant %d: variant key is required", i)
		}
		if _, exists := seen[v.VariantKey]; exists {
			return fmt.Errorf("duplicate variant key %q", v.VariantKey)
		}
		seen[v.VariantKey] = struct{}{}
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin create transcode event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertEvent = `
INSERT INTO transcode_event (
	event_id, media_id, status, attempts, last_error, locked_until
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (event_id, media_id) DO NOTHING
RETURNING id, created_at, updated_at`

	err = tx.QueryRow(ctx, insertEvent,
		event.EventID,
		event.MediaID,
		event.Status,
		event.Attempts,
		event.LastError,
		event.LockedUntil,
	).Scan(&event.ID, &event.CreatedAt, &event.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTranscodeEventExists
	}
	if err != nil {
		return fmt.Errorf("insert transcode event: %w", err)
	}

	const insertVariant = `
INSERT INTO transcode_variant (
	transcode_event_id, variant_key,
	width, height, bitrate, codec, container,
	frame_rate, playlist_key
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, created_at, updated_at`

	for i := range event.Variants {
		v := &event.Variants[i]

		err := tx.QueryRow(ctx, insertVariant,
			event.ID,
			v.VariantKey,
			v.Width,
			v.Height,
			v.Bitrate,
			v.Codec,
			v.Container,
			v.FrameRate,
			v.PlaylistKey,
		).Scan(&v.ID, &v.CreatedAt, &v.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert transcode variant %q: %w", v.VariantKey, err)
		}

		v.TranscodeEventID = event.ID
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit create transcode event: %w", err)
	}

	return nil
}

func (r *Repository) FindTranscodeEvent(
	ctx context.Context,
	eventID uuid.UUID,
	mediaID uuid.UUID,
) (*TranscodeEvent, error) {
	const findEvent = `
SELECT
	id, event_id, media_id, status, attempts, last_error, locked_until,
	created_at, updated_at
FROM transcode_event
WHERE event_id = $1 AND media_id = $2`

	var event TranscodeEvent
	err := r.db.QueryRow(ctx, findEvent, eventID, mediaID).Scan(
		&event.ID,
		&event.EventID,
		&event.MediaID,
		&event.Status,
		&event.Attempts,
		&event.LastError,
		&event.LockedUntil,
		&event.CreatedAt,
		&event.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTranscodeEventNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find transcode event: %w", err)
	}

	const findVariants = `
SELECT
	id, transcode_event_id, variant_key,
	width, height, bitrate, codec, container,
	frame_rate, playlist_key, created_at, updated_at
FROM transcode_variant
WHERE transcode_event_id = $1
ORDER BY variant_key`

	rows, err := r.db.Query(ctx, findVariants, event.ID)
	if err != nil {
		return nil, fmt.Errorf("find transcode variants: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var variant TranscodeVariant
		if err := rows.Scan(
			&variant.ID,
			&variant.TranscodeEventID,
			&variant.VariantKey,
			&variant.Width,
			&variant.Height,
			&variant.Bitrate,
			&variant.Codec,
			&variant.Container,
			&variant.FrameRate,
			&variant.PlaylistKey,
			&variant.CreatedAt,
			&variant.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan transcode variant: %w", err)
		}

		event.Variants = append(event.Variants, variant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transcode variants: %w", err)
	}

	return &event, nil
}

func (r *Repository) ClaimForProcessing(
	ctx context.Context,
	id uuid.UUID,
	lease time.Duration,
) (*TranscodeEvent, error) {
	if id == uuid.Nil {
		return nil, errors.New("transcode event ID is required")
	}
	if lease <= 0 {
		return nil, errors.New("lease must be greater than zero")
	}

	const query = `
UPDATE transcode_event
SET status = 'PROCESSING',
	attempts = attempts + 1,
	locked_until = now() + make_interval(secs => $2),
	updated_at = now()
WHERE id = $1
  AND attempts < $3
  AND (
	  status = 'FAIL'
	  OR (
		  status = 'PROCESSING'
		  AND (locked_until IS NULL OR locked_until <= now())
	  )
  )
RETURNING
	id, event_id, media_id, status, attempts, last_error, locked_until,
	created_at, updated_at`

	var event TranscodeEvent
	err := r.db.QueryRow(
		ctx,
		query,
		id,
		lease.Seconds(),
		MaxTranscodeAttempts,
	).Scan(
		&event.ID,
		&event.EventID,
		&event.MediaID,
		&event.Status,
		&event.Attempts,
		&event.LastError,
		&event.LockedUntil,
		&event.CreatedAt,
		&event.UpdatedAt,
	)
	if err == nil {
		return &event, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("claim transcode event: %w", err)
	}

	var status TranscodeStatus
	var attempts int
	lookupErr := r.db.QueryRow(ctx, `
SELECT status, attempts
FROM transcode_event
WHERE id = $1`, id).Scan(&status, &attempts)
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return nil, ErrTranscodeEventNotFound
	}
	if lookupErr != nil {
		return nil, fmt.Errorf("inspect unclaimable transcode event: %w", lookupErr)
	}

	return nil, fmt.Errorf(
		"%w: status=%s attempts=%d",
		ErrTranscodeEventNotClaimable,
		status,
		attempts,
	)
}

func (r *Repository) CompleteEvent(
	ctx context.Context,
	id uuid.UUID,
	variants []TranscodeVariant,
) error {
	if id == uuid.Nil {
		return errors.New("transcode event ID is required")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin complete transcode event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status TranscodeStatus
	err = tx.QueryRow(ctx, `
SELECT status
FROM transcode_event
WHERE id = $1
FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTranscodeEventNotFound
	}
	if err != nil {
		return fmt.Errorf("lock transcode event: %w", err)
	}

	if status == TranscodeStatusComplete {
		return nil
	}
	if status != TranscodeStatusProcessing {
		return fmt.Errorf("%w: current status %s", ErrTranscodeEventNotProcessing, status)
	}

	const upsertVariant = `
INSERT INTO transcode_variant (
	transcode_event_id, variant_key,
	width, height, bitrate, codec, container,
	frame_rate, playlist_key
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (transcode_event_id, variant_key) DO UPDATE
SET width = EXCLUDED.width,
	height = EXCLUDED.height,
	bitrate = EXCLUDED.bitrate,
	codec = EXCLUDED.codec,
	container = EXCLUDED.container,
	frame_rate = EXCLUDED.frame_rate,
	playlist_key = EXCLUDED.playlist_key
RETURNING id, created_at, updated_at`

	for i := range variants {
		v := &variants[i]
		v.TranscodeEventID = id

		err := tx.QueryRow(ctx, upsertVariant,
			id,
			v.VariantKey,
			v.Width,
			v.Height,
			v.Bitrate,
			v.Codec,
			v.Container,
			v.FrameRate,
			v.PlaylistKey,
		).Scan(&v.ID, &v.CreatedAt, &v.UpdatedAt)
		if err != nil {
			return fmt.Errorf("upsert transcode variant %q: %w", v.VariantKey, err)
		}
	}

	_, err = tx.Exec(ctx, `
UPDATE transcode_event
SET status = 'COMPLETE',
	locked_until = NULL,
	updated_at = now()
WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark transcode event complete: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit complete transcode event: %w", err)
	}

	return nil
}

func (r *Repository) FailEvent(
	ctx context.Context,
	id uuid.UUID,
	lastError string,
) error {
	if id == uuid.Nil {
		return errors.New("transcode event ID is required")
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fail transcode event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status TranscodeStatus
	err = tx.QueryRow(ctx, `
SELECT status
FROM transcode_event
WHERE id = $1
FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTranscodeEventNotFound
	}
	if err != nil {
		return fmt.Errorf("lock transcode event: %w", err)
	}

	if status == TranscodeStatusFailed {
		return nil
	}
	if status != TranscodeStatusProcessing {
		return fmt.Errorf("%w: current status %s", ErrTranscodeEventNotProcessing, status)
	}

	_, err = tx.Exec(ctx, `
UPDATE transcode_event
SET status = 'FAIL',
	last_error = $2,
	locked_until = NULL,
	updated_at = now()
WHERE id = $1`, id, lastError)
	if err != nil {
		return fmt.Errorf("mark transcode event failed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fail transcode event: %w", err)
	}

	return nil
}
