-- A library source is a container, not a single movie or series. Older
-- descendant-based projection could accidentally attach one child's media
-- identity to the source root and make the whole library render as that item.
DELETE FROM media_item_files
WHERE role = 'folder'
  AND entry_id IN (
    SELECT entry.id
    FROM entries entry
    JOIN library_sources source
      ON source.storage_id = entry.storage_id
     AND source.path = entry.path
    JOIN libraries library ON library.id = source.library_id
    WHERE library.type IN ('movies', 'tv')
  );
