BEGIN;

CREATE TABLE IF NOT EXISTS transcode_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    event_id UUID NOT NULL,
    media_id UUID NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'PROCESSING'
        CHECK (status IN ('PROCESSING', 'FAIL', 'COMPLETE')),

    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error TEXT,
    locked_until TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_transcode_event_event_media
        UNIQUE (event_id, media_id)
);

CREATE TABLE IF NOT EXISTS transcode_variant (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    transcode_event_id UUID NOT NULL
        REFERENCES transcode_event(id) ON DELETE CASCADE,

    variant_key VARCHAR(50) NOT NULL,

    width INTEGER CHECK (width IS NULL OR width > 0),
    height INTEGER CHECK (height IS NULL OR height > 0),
    bitrate INTEGER CHECK (bitrate IS NULL OR bitrate > 0),
    codec VARCHAR(20),
    container VARCHAR(20),
    frame_rate NUMERIC(6, 2) CHECK (frame_rate IS NULL OR frame_rate > 0),
    playlist_key VARCHAR(500),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_transcode_variant_event_key
        UNIQUE (transcode_event_id, variant_key)
);

CREATE INDEX IF NOT EXISTS idx_transcode_event_event_id
    ON transcode_event (event_id);

CREATE INDEX IF NOT EXISTS idx_transcode_event_media_id
    ON transcode_event (media_id);

CREATE INDEX IF NOT EXISTS idx_transcode_event_status
    ON transcode_event (status);

CREATE INDEX IF NOT EXISTS idx_transcode_variant_event_id
    ON transcode_variant (transcode_event_id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_transcode_variant_playlist_key
    ON transcode_variant (playlist_key)
    WHERE playlist_key IS NOT NULL;

CREATE OR REPLACE FUNCTION set_transcode_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS transcode_event_set_updated_at ON transcode_event;
CREATE TRIGGER transcode_event_set_updated_at
BEFORE UPDATE ON transcode_event
FOR EACH ROW
EXECUTE FUNCTION set_transcode_updated_at();

DROP TRIGGER IF EXISTS transcode_variant_set_updated_at ON transcode_variant;
CREATE TRIGGER transcode_variant_set_updated_at
BEFORE UPDATE ON transcode_variant
FOR EACH ROW
EXECUTE FUNCTION set_transcode_updated_at();

COMMIT;
