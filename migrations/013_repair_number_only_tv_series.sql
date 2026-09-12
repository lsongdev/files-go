-- Older cataloging treated a filename such as S01E01.mp4 as the series title
-- even when its parent hierarchy contained the real title. Remove only these
-- automatic fallback trees. Startup backfill recreates their associations
-- using the containing series directory; manual/TMDB matches are untouched.
DELETE FROM media_items
WHERE type = 'series'
  AND match_source = 'filename'
  AND lower(COALESCE(external_id, '')) GLOB 'filename:s[0-9]*e[0-9]*';
