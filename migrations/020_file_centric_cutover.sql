-- Old media identities and their playback data are intentionally discarded.
-- The scanner rebuilds medias only from configured library sources.
DROP TABLE IF EXISTS playback_states;
DELETE FROM artifacts WHERE media_id IS NOT NULL;
ALTER TABLE artifacts DROP COLUMN media_id;
DELETE FROM medias;
DROP TABLE IF EXISTS media_item_files;
DROP TABLE IF EXISTS media_match_suppressions;
DROP TABLE IF EXISTS media_items;
DROP TABLE IF EXISTS media_files;

CREATE TABLE playback_states (
    user_id     TEXT NOT NULL,
    entry_id    TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    position_ms INTEGER NOT NULL DEFAULT 0,
    played      INTEGER NOT NULL DEFAULT 0,
    updated_at  DATETIME NOT NULL,
    PRIMARY KEY(user_id, entry_id)
);
CREATE INDEX idx_playback_states_continue
ON playback_states(user_id, played, updated_at DESC)
WHERE position_ms > 0;

DELETE FROM jobs WHERE type='process_entry';
DELETE FROM scan_checkpoints;
UPDATE storages SET state='interrupted', scan_scope='', scan_error='media model rebuild pending'
WHERE state IN ('online','scanning','interrupted','error');
