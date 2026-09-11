CREATE TABLE playback_states (
    user_id     TEXT NOT NULL,
    media_id    TEXT NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    position_ms INTEGER NOT NULL DEFAULT 0,
    played      INTEGER NOT NULL DEFAULT 0,
    updated_at  DATETIME NOT NULL,
    PRIMARY KEY(user_id, media_id)
);

CREATE INDEX idx_playback_states_continue
ON playback_states(user_id, played, updated_at DESC)
WHERE position_ms > 0;
