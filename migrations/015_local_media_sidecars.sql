-- Folder artwork is a local media sidecar, not an independent photo item.
DELETE FROM media_item_files
WHERE role='photo' AND entry_id IN (
    SELECT id FROM entries WHERE lower(name) IN (
        'folder.jpg','folder.jpeg','folder.png','poster.jpg','poster.jpeg','poster.png',
        'cover.jpg','cover.jpeg','cover.png','backdrop.jpg','backdrop.jpeg','backdrop.png',
        'fanart.jpg','fanart.jpeg','fanart.png','background.jpg','background.jpeg','background.png'
    )
);

DELETE FROM media_items
WHERE type='photo' AND NOT EXISTS (
    SELECT 1 FROM media_item_files WHERE media_item_files.media_id=media_items.id
);
