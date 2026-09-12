ALTER TABLE media_files ADD COLUMN taken_at DATETIME;
ALTER TABLE media_files ADD COLUMN camera TEXT NOT NULL DEFAULT '';
ALTER TABLE media_files ADD COLUMN latitude REAL;
ALTER TABLE media_files ADD COLUMN longitude REAL;

CREATE INDEX idx_media_files_taken_at
ON media_files(taken_at)
WHERE taken_at IS NOT NULL;
