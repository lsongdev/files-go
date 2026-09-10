CREATE TABLE media_files (
    entry_id     TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    duration_ms  INTEGER,
    container    TEXT,
    width        INTEGER,
    height       INTEGER,
    video_codec  TEXT,
    audio_codec  TEXT,
    bitrate      INTEGER,
    metadata     TEXT NOT NULL DEFAULT '{}',
    updated_at   DATETIME NOT NULL
);

CREATE INDEX idx_media_files_kind ON media_files(kind);

CREATE TABLE artifacts (
    id               TEXT PRIMARY KEY,
    entry_id         TEXT REFERENCES entries(id) ON DELETE CASCADE,
    media_id         TEXT,
    type             TEXT NOT NULL,
    variant          TEXT NOT NULL DEFAULT '',
    key              TEXT NOT NULL,
    mime             TEXT,
    size             INTEGER NOT NULL DEFAULT 0,
    created_at       DATETIME NOT NULL,
    last_accessed_at DATETIME NOT NULL,
    UNIQUE(type, key)
);

CREATE INDEX idx_artifacts_entry_type_variant
ON artifacts(entry_id, type, variant);

CREATE INDEX idx_artifacts_gc ON artifacts(last_accessed_at);

