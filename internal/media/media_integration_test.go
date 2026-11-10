package media

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	if testing.Short() {
		fmt.Println("skipping media integration tests in short mode")
		return 0
	}

	ctx := context.Background()
	containerName := fmt.Sprintf("transcoder-media-test-%d", os.Getpid())

	dsn, cleanup, err := startPostgres(ctx, containerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "media integration setup: %v\n", err)
		return 1
	}
	defer cleanup()

	if err := applySchema(ctx, containerName); err != nil {
		fmt.Fprintf(os.Stderr, "apply schema: %v\n", err)
		return 1
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create database pool: %v\n", err)
		return 1
	}
	defer pool.Close()

	testPool = pool
	return m.Run()
}

func startPostgres(ctx context.Context, name string) (string, func(), error) {
	// Remove a container left behind by an interrupted run.
	_ = exec.Command("docker", "rm", "-f", name).Run()

	run := exec.CommandContext(
		ctx,
		"docker", "run", "--rm", "-d",
		"--name", name,
		"-e", "POSTGRES_USER=postgres",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-e", "POSTGRES_DB=transcoder_test",
		"-p", "127.0.0.1::5432",
		"postgres:17-alpine",
	)
	out, err := run.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("docker run: %w: %s", err, strings.TrimSpace(string(out)))
	}

	containerID := strings.TrimSpace(string(out))
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		check := exec.CommandContext(
			ctx,
			"docker", "exec", containerID,
			"psql", "-U", "postgres", "-d", "transcoder_test", "-Atc", "select 1",
		)
		if err := check.Run(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cleanup()
			return "", nil, errors.New("timed out waiting for PostgreSQL")
		}
		time.Sleep(500 * time.Millisecond)
	}

	portOut, err := exec.CommandContext(ctx, "docker", "port", containerID, "5432/tcp").Output()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("docker port: %w", err)
	}

	addr := strings.TrimSpace(strings.Split(string(portOut), "\n")[0])
	dsn := fmt.Sprintf(
		"postgres://postgres:postgres@%s/transcoder_test?sslmode=disable",
		addr,
	)

	return dsn, cleanup, nil
}

func applySchema(ctx context.Context, containerID string) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("resolve test file path")
	}

	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "schema.sql")
	schema, err := os.Open(schemaPath)
	if err != nil {
		return fmt.Errorf("open schema: %w", err)
	}
	defer schema.Close()

	cmd := exec.CommandContext(
		ctx,
		"docker", "exec", "-i", containerID,
		"psql", "-U", "postgres", "-d", "transcoder_test",
		"-v", "ON_ERROR_STOP=1", "-q",
	)
	cmd.Stdin = schema

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql schema: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

func resetDatabase(t *testing.T) {
	t.Helper()

	_, err := testPool.Exec(
		context.Background(),
		`TRUNCATE TABLE transcode_variant, transcode_event RESTART IDENTITY CASCADE`,
	)
	if err != nil {
		t.Fatalf("reset database: %v", err)
	}
}

func testVariants() []TranscodeVariant {
	width1080 := 1920
	height1080 := 1080
	bitrate1080 := 5000
	width720 := 1280
	height720 := 720
	bitrate720 := 2800
	codec := "h264"
	container := "hls"
	frameRate := 30.0

	return []TranscodeVariant{
		{
			VariantKey: VariantKey1080p,
			Width:      &width1080,
			Height:     &height1080,
			Bitrate:    &bitrate1080,
			Codec:      &codec,
			Container:  &container,
			FrameRate:  &frameRate,
		},
		{
			VariantKey: VariantKey720p,
			Width:      &width720,
			Height:     &height720,
			Bitrate:    &bitrate720,
			Codec:      &codec,
			Container:  &container,
			FrameRate:  &frameRate,
		},
	}
}

func newEvent(
	eventID uuid.UUID,
	mediaID uuid.UUID,
	status TranscodeStatus,
	attempts int,
	lockedUntil *time.Time,
) *TranscodeEvent {
	return &TranscodeEvent{
		EventID:     eventID,
		MediaID:     mediaID,
		Status:      status,
		Attempts:    attempts,
		LockedUntil: lockedUntil,
		Variants:    testVariants(),
	}
}

func mustCreateEvent(t *testing.T, repo *Repository, event *TranscodeEvent) {
	t.Helper()

	if err := repo.CreateEvent(context.Background(), event); err != nil {
		t.Fatalf("create event: %v", err)
	}
}

func mustFindEvent(
	t *testing.T,
	repo *Repository,
	eventID uuid.UUID,
	mediaID uuid.UUID,
) *TranscodeEvent {
	t.Helper()

	event, err := repo.FindTranscodeEvent(context.Background(), eventID, mediaID)
	if err != nil {
		t.Fatalf("find event: %v", err)
	}

	return event
}

func mustHandle(
	t *testing.T,
	service *Service,
	eventID uuid.UUID,
	mediaID uuid.UUID,
	variants []TranscodeVariant,
) HandleResult {
	t.Helper()

	result, err := service.HandleTranscodeEvent(
		context.Background(),
		eventID,
		mediaID,
		variants,
	)
	if err != nil {
		t.Fatalf("handle transcode event: %v", err)
	}

	return result
}

func requireOutcome(t *testing.T, result HandleResult, want TranscodeOutcome) {
	t.Helper()

	if result.Outcome != want {
		t.Fatalf("outcome = %q, want %q", result.Outcome, want)
	}
}

func requireStatus(t *testing.T, event *TranscodeEvent, want TranscodeStatus) {
	t.Helper()

	if event.Status != want {
		t.Fatalf("status = %q, want %q", event.Status, want)
	}
}

func requireVariants(t *testing.T, event *TranscodeEvent, want int) {
	t.Helper()

	if len(event.Variants) != want {
		t.Fatalf("variants = %d, want %d", len(event.Variants), want)
	}
}

func TestHandleTranscodeEventCreatesNewEvent(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	repo := NewRepository(testPool)
	service := NewService(repo, time.Hour)
	eventID := uuid.New()
	mediaID := uuid.New()
	before := time.Now()

	result := mustHandle(t, service, eventID, mediaID, testVariants())

	requireOutcome(t, result, TranscodeOutcomeProcess)
	if result.Event == nil {
		t.Fatal("event is nil")
	}
	if result.Event.ID == uuid.Nil {
		t.Fatal("event ID was not populated")
	}
	requireStatus(t, result.Event, TranscodeStatusProcessing)
	if result.Event.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", result.Event.Attempts)
	}
	if result.Event.LockedUntil == nil || !result.Event.LockedUntil.After(before) {
		t.Fatalf("locked_until = %v, want a future time", result.Event.LockedUntil)
	}
	requireVariants(t, result.Event, 2)

	stored, err := repo.FindTranscodeEvent(ctx, eventID, mediaID)
	if err != nil {
		t.Fatalf("find stored event: %v", err)
	}
	if stored.ID != result.Event.ID {
		t.Fatalf("stored ID = %s, want %s", stored.ID, result.Event.ID)
	}
	requireStatus(t, stored, TranscodeStatusProcessing)
	if stored.Attempts != 1 {
		t.Fatalf("stored attempts = %d, want 1", stored.Attempts)
	}
	requireVariants(t, stored, 2)

	keys := make(map[string]bool, len(stored.Variants))
	for _, variant := range stored.Variants {
		if variant.ID == uuid.Nil {
			t.Fatalf("variant %q has no ID", variant.VariantKey)
		}
		if variant.TranscodeEventID != stored.ID {
			t.Fatalf(
				"variant %q event ID = %s, want %s",
				variant.VariantKey,
				variant.TranscodeEventID,
				stored.ID,
			)
		}
		keys[variant.VariantKey] = true
	}
	if !keys[VariantKey1080p] || !keys[VariantKey720p] {
		t.Fatalf("missing variant rows: %v", keys)
	}
}

func TestHandleTranscodeEventIgnoresActiveProcessing(t *testing.T) {
	resetDatabase(t)

	repo := NewRepository(testPool)
	service := NewService(repo, time.Hour)
	eventID := uuid.New()
	mediaID := uuid.New()
	lockedUntil := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)

	existing := newEvent(
		eventID,
		mediaID,
		TranscodeStatusProcessing,
		1,
		&lockedUntil,
	)
	mustCreateEvent(t, repo, existing)

	result := mustHandle(t, service, eventID, mediaID, nil)

	requireOutcome(t, result, TranscodeOutcomeIgnored)
	if result.Event.Attempts != 1 {
		t.Fatalf("result attempts = %d, want 1", result.Event.Attempts)
	}

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusProcessing)
	if stored.Attempts != 1 {
		t.Fatalf("stored attempts = %d, want 1", stored.Attempts)
	}
	if stored.LockedUntil == nil || !stored.LockedUntil.Equal(lockedUntil) {
		t.Fatalf("stored locked_until = %v, want %v", stored.LockedUntil, lockedUntil)
	}
	requireVariants(t, stored, 2)
}

func TestHandleTranscodeEventClaimsExpiredProcessing(t *testing.T) {
	resetDatabase(t)

	repo := NewRepository(testPool)
	service := NewService(repo, time.Hour)
	eventID := uuid.New()
	mediaID := uuid.New()
	lockedUntil := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)

	mustCreateEvent(t, repo, newEvent(
		eventID,
		mediaID,
		TranscodeStatusProcessing,
		1,
		&lockedUntil,
	))

	result := mustHandle(t, service, eventID, mediaID, nil)

	requireOutcome(t, result, TranscodeOutcomeProcess)
	if result.Event.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Event.Attempts)
	}
	if result.Event.LockedUntil == nil || !result.Event.LockedUntil.After(time.Now()) {
		t.Fatalf("locked_until = %v, want a future time", result.Event.LockedUntil)
	}
	requireVariants(t, result.Event, 2)

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusProcessing)
	if stored.Attempts != 2 {
		t.Fatalf("stored attempts = %d, want 2", stored.Attempts)
	}
}

func TestHandleTranscodeEventIgnoresComplete(t *testing.T) {
	resetDatabase(t)

	repo := NewRepository(testPool)
	service := NewService(repo, time.Hour)
	eventID := uuid.New()
	mediaID := uuid.New()

	mustCreateEvent(t, repo, newEvent(
		eventID,
		mediaID,
		TranscodeStatusComplete,
		2,
		nil,
	))

	result := mustHandle(t, service, eventID, mediaID, nil)

	requireOutcome(t, result, TranscodeOutcomeIgnored)
	requireStatus(t, result.Event, TranscodeStatusComplete)

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusComplete)
	if stored.Attempts != 2 {
		t.Fatalf("stored attempts = %d, want 2", stored.Attempts)
	}
}

func TestHandleTranscodeEventRetriesFailedEvent(t *testing.T) {
	resetDatabase(t)

	repo := NewRepository(testPool)
	service := NewService(repo, time.Hour)
	eventID := uuid.New()
	mediaID := uuid.New()
	lastError := "first failure"

	existing := newEvent(eventID, mediaID, TranscodeStatusFailed, 1, nil)
	existing.LastError = &lastError
	mustCreateEvent(t, repo, existing)

	result := mustHandle(t, service, eventID, mediaID, nil)

	requireOutcome(t, result, TranscodeOutcomeProcess)
	if result.Event.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Event.Attempts)
	}
	if result.Event.LastError == nil || *result.Event.LastError != lastError {
		t.Fatalf("last_error = %v, want %q", result.Event.LastError, lastError)
	}
	requireVariants(t, result.Event, 2)

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusProcessing)
	if stored.Attempts != 2 {
		t.Fatalf("stored attempts = %d, want 2", stored.Attempts)
	}
	if stored.LastError == nil || *stored.LastError != lastError {
		t.Fatalf("stored last_error = %v, want %q", stored.LastError, lastError)
	}
}

func TestHandleTranscodeEventDeadLettersExhaustedEvent(t *testing.T) {
	resetDatabase(t)

	repo := NewRepository(testPool)
	service := NewService(repo, time.Hour)
	eventID := uuid.New()
	mediaID := uuid.New()
	lastError := "all attempts failed"

	existing := newEvent(
		eventID,
		mediaID,
		TranscodeStatusFailed,
		MaxTranscodeAttempts,
		nil,
	)
	existing.LastError = &lastError
	mustCreateEvent(t, repo, existing)

	result := mustHandle(t, service, eventID, mediaID, nil)

	requireOutcome(t, result, TranscodeOutcomeDeadLetter)
	if result.Event.Attempts != MaxTranscodeAttempts {
		t.Fatalf(
			"attempts = %d, want %d",
			result.Event.Attempts,
			MaxTranscodeAttempts,
		)
	}

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusFailed)
	if stored.Attempts != MaxTranscodeAttempts {
		t.Fatalf(
			"stored attempts = %d, want %d",
			stored.Attempts,
			MaxTranscodeAttempts,
		)
	}
	if stored.LockedUntil != nil {
		t.Fatalf("stored locked_until = %v, want nil", stored.LockedUntil)
	}
}

type racingRepository struct {
	*Repository
	beforeCreate func(ctx context.Context) error
	done         bool
}

func (r *racingRepository) CreateEvent(ctx context.Context, event *TranscodeEvent) error {
	if !r.done {
		r.done = true
		if err := r.beforeCreate(ctx); err != nil {
			return err
		}
	}

	return r.Repository.CreateEvent(ctx, event)
}

func TestHandleTranscodeEventCreateRace(t *testing.T) {
	t.Run("winner is still processing", func(t *testing.T) {
		resetDatabase(t)

		repo := NewRepository(testPool)
		eventID := uuid.New()
		mediaID := uuid.New()
		lockedUntil := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		winner := newEvent(
			eventID,
			mediaID,
			TranscodeStatusProcessing,
			1,
			&lockedUntil,
		)

		racing := &racingRepository{
			Repository: repo,
			beforeCreate: func(ctx context.Context) error {
				return repo.CreateEvent(ctx, winner)
			},
		}
		service := NewService(racing, time.Hour)

		result := mustHandle(t, service, eventID, mediaID, testVariants())

		requireOutcome(t, result, TranscodeOutcomeIgnored)

		stored := mustFindEvent(t, repo, eventID, mediaID)
		requireStatus(t, stored, TranscodeStatusProcessing)
		if stored.Attempts != 1 {
			t.Fatalf("stored attempts = %d, want 1", stored.Attempts)
		}
		requireVariants(t, stored, 2)
	})

	t.Run("winner is retryable", func(t *testing.T) {
		resetDatabase(t)

		repo := NewRepository(testPool)
		eventID := uuid.New()
		mediaID := uuid.New()
		lastError := "winner failed"
		winner := newEvent(eventID, mediaID, TranscodeStatusFailed, 1, nil)
		winner.LastError = &lastError

		racing := &racingRepository{
			Repository: repo,
			beforeCreate: func(ctx context.Context) error {
				return repo.CreateEvent(ctx, winner)
			},
		}
		service := NewService(racing, time.Hour)

		result := mustHandle(t, service, eventID, mediaID, testVariants())

		requireOutcome(t, result, TranscodeOutcomeProcess)
		if result.Event.Attempts != 2 {
			t.Fatalf("attempts = %d, want 2", result.Event.Attempts)
		}
		requireVariants(t, result.Event, 2)
	})
}

func TestCompleteEventWritesVariantsAndIsIdempotent(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	repo := NewRepository(testPool)
	service := NewService(repo, time.Hour)
	eventID := uuid.New()
	mediaID := uuid.New()

	result := mustHandle(t, service, eventID, mediaID, testVariants())
	requireOutcome(t, result, TranscodeOutcomeProcess)

	variants := make([]TranscodeVariant, len(result.Event.Variants))
	copy(variants, result.Event.Variants)

	playlistKeys := make(map[string]string, len(variants))
	for i := range variants {
		key := fmt.Sprintf("media/%s/%s/index.m3u8", mediaID, variants[i].VariantKey)
		variants[i].PlaylistKey = &key
		playlistKeys[variants[i].VariantKey] = key
	}

	if err := repo.CompleteEvent(ctx, result.Event.ID, variants); err != nil {
		t.Fatalf("complete event: %v", err)
	}

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusComplete)
	if stored.Attempts != 1 {
		t.Fatalf("stored attempts = %d, want 1", stored.Attempts)
	}
	if stored.LockedUntil != nil {
		t.Fatalf("stored locked_until = %v, want nil", stored.LockedUntil)
	}
	if stored.LastError != nil {
		t.Fatalf("stored last_error = %v, want nil", stored.LastError)
	}
	requireVariants(t, stored, 2)

	for _, variant := range stored.Variants {
		wantKey := playlistKeys[variant.VariantKey]
		if variant.PlaylistKey == nil || *variant.PlaylistKey != wantKey {
			t.Fatalf(
				"variant %q playlist_key = %v, want %q",
				variant.VariantKey,
				variant.PlaylistKey,
				wantKey,
			)
		}
	}

	if err := repo.CompleteEvent(ctx, result.Event.ID, variants); err != nil {
		t.Fatalf("complete event again: %v", err)
	}

	result = mustHandle(t, service, eventID, mediaID, nil)
	requireOutcome(t, result, TranscodeOutcomeIgnored)
	requireStatus(t, result.Event, TranscodeStatusComplete)
}

func TestCompleteEventKeepsLastError(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	repo := NewRepository(testPool)
	service := NewService(repo, time.Hour)
	eventID := uuid.New()
	mediaID := uuid.New()
	lastError := "failed before"

	existing := newEvent(eventID, mediaID, TranscodeStatusFailed, 1, nil)
	existing.LastError = &lastError
	mustCreateEvent(t, repo, existing)

	result := mustHandle(t, service, eventID, mediaID, nil)
	requireOutcome(t, result, TranscodeOutcomeProcess)

	if err := repo.CompleteEvent(ctx, result.Event.ID, result.Event.Variants); err != nil {
		t.Fatalf("complete event: %v", err)
	}

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusComplete)
	if stored.LastError == nil || *stored.LastError != lastError {
		t.Fatalf("stored last_error = %v, want %q", stored.LastError, lastError)
	}
}

func TestFailRetryUntilDeadLetter(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	repo := NewRepository(testPool)
	service := NewService(repo, time.Hour)
	eventID := uuid.New()
	mediaID := uuid.New()

	result := mustHandle(t, service, eventID, mediaID, testVariants())
	requireOutcome(t, result, TranscodeOutcomeProcess)
	eventRowID := result.Event.ID

	failures := []string{"first failure", "second failure", "third failure"}

	for i, failure := range failures {
		if err := repo.FailEvent(ctx, eventRowID, failure); err != nil {
			t.Fatalf("fail event %d: %v", i+1, err)
		}

		stored := mustFindEvent(t, repo, eventID, mediaID)
		requireStatus(t, stored, TranscodeStatusFailed)
		if stored.Attempts != i+1 {
			t.Fatalf("stored attempts = %d, want %d", stored.Attempts, i+1)
		}
		if stored.LastError == nil || *stored.LastError != failure {
			t.Fatalf("stored last_error = %v, want %q", stored.LastError, failure)
		}
		if stored.LockedUntil != nil {
			t.Fatalf("stored locked_until = %v, want nil", stored.LockedUntil)
		}

		if i < len(failures)-1 {
			result = mustHandle(t, service, eventID, mediaID, nil)
			requireOutcome(t, result, TranscodeOutcomeProcess)
			if result.Event.Attempts != i+2 {
				t.Fatalf(
					"retry attempts = %d, want %d",
					result.Event.Attempts,
					i+2,
				)
			}
			if result.Event.LastError == nil || *result.Event.LastError != failure {
				t.Fatalf(
					"retry last_error = %v, want %q",
					result.Event.LastError,
					failure,
				)
			}
			requireVariants(t, result.Event, 2)
		}
	}

	result = mustHandle(t, service, eventID, mediaID, nil)
	requireOutcome(t, result, TranscodeOutcomeDeadLetter)
	if result.Event.Attempts != MaxTranscodeAttempts {
		t.Fatalf(
			"dead-letter attempts = %d, want %d",
			result.Event.Attempts,
			MaxTranscodeAttempts,
		)
	}

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusFailed)
	if stored.Attempts != MaxTranscodeAttempts {
		t.Fatalf(
			"stored attempts = %d, want %d",
			stored.Attempts,
			MaxTranscodeAttempts,
		)
	}
	if stored.LastError == nil || *stored.LastError != "third failure" {
		t.Fatalf(
			"stored last_error = %v, want %q",
			stored.LastError,
			"third failure",
		)
	}
}

func TestClaimForProcessingCases(t *testing.T) {
	ctx := context.Background()
	repo := NewRepository(testPool)

	t.Run("not found", func(t *testing.T) {
		resetDatabase(t)

		_, err := repo.ClaimForProcessing(ctx, uuid.New(), time.Hour)
		if !errors.Is(err, ErrTranscodeEventNotFound) {
			t.Fatalf("error = %v, want %v", err, ErrTranscodeEventNotFound)
		}
	})

	t.Run("complete event", func(t *testing.T) {
		resetDatabase(t)

		eventID := uuid.New()
		mediaID := uuid.New()
		event := newEvent(eventID, mediaID, TranscodeStatusComplete, 1, nil)
		mustCreateEvent(t, repo, event)

		_, err := repo.ClaimForProcessing(ctx, event.ID, time.Hour)
		if !errors.Is(err, ErrTranscodeEventAlreadyComplete) {
			t.Fatalf("error = %v, want %v", err, ErrTranscodeEventAlreadyComplete)
		}
	})

	t.Run("active processing lock", func(t *testing.T) {
		resetDatabase(t)

		eventID := uuid.New()
		mediaID := uuid.New()
		lockedUntil := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		event := newEvent(
			eventID,
			mediaID,
			TranscodeStatusProcessing,
			1,
			&lockedUntil,
		)
		mustCreateEvent(t, repo, event)

		_, err := repo.ClaimForProcessing(ctx, event.ID, time.Hour)
		if !errors.Is(err, ErrTranscodeEventLocked) {
			t.Fatalf("error = %v, want %v", err, ErrTranscodeEventLocked)
		}
	})

	t.Run("expired processing lock", func(t *testing.T) {
		resetDatabase(t)

		eventID := uuid.New()
		mediaID := uuid.New()
		lockedUntil := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
		event := newEvent(
			eventID,
			mediaID,
			TranscodeStatusProcessing,
			1,
			&lockedUntil,
		)
		mustCreateEvent(t, repo, event)

		claimed, err := repo.ClaimForProcessing(ctx, event.ID, time.Hour)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if claimed.Attempts != 2 {
			t.Fatalf("attempts = %d, want 2", claimed.Attempts)
		}
		if claimed.LockedUntil == nil || !claimed.LockedUntil.After(time.Now()) {
			t.Fatalf("locked_until = %v, want a future time", claimed.LockedUntil)
		}
	})

	t.Run("null processing lock", func(t *testing.T) {
		resetDatabase(t)

		eventID := uuid.New()
		mediaID := uuid.New()
		event := newEvent(eventID, mediaID, TranscodeStatusProcessing, 1, nil)
		mustCreateEvent(t, repo, event)

		claimed, err := repo.ClaimForProcessing(ctx, event.ID, time.Hour)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if claimed.Attempts != 2 {
			t.Fatalf("attempts = %d, want 2", claimed.Attempts)
		}
	})

	t.Run("failed event with attempts left", func(t *testing.T) {
		resetDatabase(t)

		eventID := uuid.New()
		mediaID := uuid.New()
		lastError := "retry me"
		event := newEvent(eventID, mediaID, TranscodeStatusFailed, 1, nil)
		event.LastError = &lastError
		mustCreateEvent(t, repo, event)

		claimed, err := repo.ClaimForProcessing(ctx, event.ID, time.Hour)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if claimed.Attempts != 2 {
			t.Fatalf("attempts = %d, want 2", claimed.Attempts)
		}
		if claimed.LastError == nil || *claimed.LastError != lastError {
			t.Fatalf("last_error = %v, want %q", claimed.LastError, lastError)
		}
	})

	t.Run("failed event at max attempts", func(t *testing.T) {
		resetDatabase(t)

		eventID := uuid.New()
		mediaID := uuid.New()
		event := newEvent(
			eventID,
			mediaID,
			TranscodeStatusFailed,
			MaxTranscodeAttempts,
			nil,
		)
		mustCreateEvent(t, repo, event)

		_, err := repo.ClaimForProcessing(ctx, event.ID, time.Hour)
		if !errors.Is(err, ErrTranscodeEventAttemptsExhausted) {
			t.Fatalf(
				"error = %v, want %v",
				err,
				ErrTranscodeEventAttemptsExhausted,
			)
		}
	})

	t.Run("active third attempt", func(t *testing.T) {
		resetDatabase(t)

		eventID := uuid.New()
		mediaID := uuid.New()
		lockedUntil := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		event := newEvent(
			eventID,
			mediaID,
			TranscodeStatusProcessing,
			MaxTranscodeAttempts,
			&lockedUntil,
		)
		mustCreateEvent(t, repo, event)

		_, err := repo.ClaimForProcessing(ctx, event.ID, time.Hour)
		if !errors.Is(err, ErrTranscodeEventLocked) {
			t.Fatalf("error = %v, want %v", err, ErrTranscodeEventLocked)
		}
	})

	t.Run("expired third attempt", func(t *testing.T) {
		resetDatabase(t)

		eventID := uuid.New()
		mediaID := uuid.New()
		lockedUntil := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
		event := newEvent(
			eventID,
			mediaID,
			TranscodeStatusProcessing,
			MaxTranscodeAttempts,
			&lockedUntil,
		)
		mustCreateEvent(t, repo, event)

		_, err := repo.ClaimForProcessing(ctx, event.ID, time.Hour)
		if !errors.Is(err, ErrTranscodeEventAttemptsExhausted) {
			t.Fatalf(
				"error = %v, want %v",
				err,
				ErrTranscodeEventAttemptsExhausted,
			)
		}
	})
}

func TestCompleteEventRejectsFailedEvent(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	repo := NewRepository(testPool)
	eventID := uuid.New()
	mediaID := uuid.New()
	lastError := "cannot complete"

	event := newEvent(eventID, mediaID, TranscodeStatusFailed, 1, nil)
	event.LastError = &lastError
	mustCreateEvent(t, repo, event)

	err := repo.CompleteEvent(ctx, event.ID, testVariants())
	if !errors.Is(err, ErrTranscodeEventNotProcessing) {
		t.Fatalf("error = %v, want %v", err, ErrTranscodeEventNotProcessing)
	}

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusFailed)
}

func TestFailEventOnCompleteIsNoop(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	repo := NewRepository(testPool)
	eventID := uuid.New()
	mediaID := uuid.New()

	event := newEvent(eventID, mediaID, TranscodeStatusComplete, 1, nil)
	mustCreateEvent(t, repo, event)

	if err := repo.FailEvent(ctx, event.ID, "late failure"); err != nil {
		t.Fatalf("fail completed event: %v", err)
	}

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusComplete)
	if stored.LastError != nil {
		t.Fatalf("stored last_error = %v, want nil", stored.LastError)
	}
}

func TestFailEventIsIdempotent(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	repo := NewRepository(testPool)
	eventID := uuid.New()
	mediaID := uuid.New()

	event := newEvent(eventID, mediaID, TranscodeStatusProcessing, 1, nil)
	mustCreateEvent(t, repo, event)

	if err := repo.FailEvent(ctx, event.ID, "first failure"); err != nil {
		t.Fatalf("first fail: %v", err)
	}
	if err := repo.FailEvent(ctx, event.ID, "second failure"); err != nil {
		t.Fatalf("second fail: %v", err)
	}

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireStatus(t, stored, TranscodeStatusFailed)
	if stored.LastError == nil || *stored.LastError != "first failure" {
		t.Fatalf(
			"stored last_error = %v, want %q",
			stored.LastError,
			"first failure",
		)
	}
}

func TestCreateEventRejectsDuplicateEvent(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	repo := NewRepository(testPool)
	eventID := uuid.New()
	mediaID := uuid.New()

	first := newEvent(eventID, mediaID, TranscodeStatusProcessing, 1, nil)
	mustCreateEvent(t, repo, first)

	second := newEvent(eventID, mediaID, TranscodeStatusProcessing, 1, nil)
	err := repo.CreateEvent(ctx, second)
	if !errors.Is(err, ErrTranscodeEventExists) {
		t.Fatalf("error = %v, want %v", err, ErrTranscodeEventExists)
	}

	stored := mustFindEvent(t, repo, eventID, mediaID)
	requireVariants(t, stored, 2)
}

func TestCreateEventRejectsDuplicateVariantKeys(t *testing.T) {
	resetDatabase(t)

	ctx := context.Background()
	repo := NewRepository(testPool)
	eventID := uuid.New()
	mediaID := uuid.New()

	event := &TranscodeEvent{
		EventID:  eventID,
		MediaID:  mediaID,
		Status:   TranscodeStatusProcessing,
		Attempts: 1,
		Variants: []TranscodeVariant{
			{VariantKey: VariantKey1080p},
			{VariantKey: VariantKey1080p},
		},
	}

	if err := repo.CreateEvent(ctx, event); err == nil {
		t.Fatal("expected duplicate variant key error")
	}

	if _, err := repo.FindTranscodeEvent(ctx, eventID, mediaID); !errors.Is(
		err,
		ErrTranscodeEventNotFound,
	) {
		t.Fatalf("find error = %v, want %v", err, ErrTranscodeEventNotFound)
	}
}
