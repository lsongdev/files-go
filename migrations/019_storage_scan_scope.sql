-- A resumable scan is valid only for the exact set of configured library roots
-- that created its checkpoints.
ALTER TABLE storages ADD COLUMN scan_scope TEXT NOT NULL DEFAULT '';
