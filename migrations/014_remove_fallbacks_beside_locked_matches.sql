-- A manual TMDB match could previously be followed by the local cataloger,
-- leaving a second filename-derived hierarchy attached to the same entry.
-- Keep the locked association and remove only automatic filename associations.
DELETE FROM media_item_files AS fallback
WHERE fallback.role IN ('video', 'season', 'series')
  AND fallback.media_id IN (SELECT id FROM media_items WHERE match_source='filename')
  AND EXISTS (
    SELECT 1 FROM media_item_files locked_file
    JOIN media_items locked_media ON locked_media.id=locked_file.media_id
    WHERE locked_file.entry_id=fallback.entry_id
      AND locked_file.role='video'
      AND locked_media.match_locked=1
  );
