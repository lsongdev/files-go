UPDATE jobs
SET state = 'pending',
    attempts = 0,
    run_after = NULL,
    lease_until = NULL,
    started_at = NULL,
    finished_at = NULL,
    error = NULL
WHERE type = 'process_entry'
  AND state = 'failed'
  AND error LIKE 'processor ffprobe:%'
  AND EXISTS (
    SELECT 1
    FROM entries
    WHERE entries.id = json_extract(jobs.payload, '$.entryId')
      AND lower(entries.extension) = 'ts'
  );
