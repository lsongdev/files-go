CREATE VIRTUAL TABLE entry_search USING fts5(
    entry_id UNINDEXED,
    name,
    title,
    metadata,
    tokenize = 'unicode61 remove_diacritics 2'
);

INSERT INTO entry_search(entry_id, name, title, metadata)
SELECT id, name, '', '' FROM entries;

CREATE TRIGGER entries_search_insert AFTER INSERT ON entries BEGIN
    INSERT INTO entry_search(entry_id, name, title, metadata)
    VALUES (new.id, new.name, '', '');
END;

CREATE TRIGGER entries_search_update AFTER UPDATE OF id, name ON entries BEGIN
    UPDATE entry_search
    SET entry_id = new.id, name = new.name
    WHERE entry_id = old.id;
END;

CREATE TRIGGER entries_search_delete AFTER DELETE ON entries BEGIN
    DELETE FROM entry_search WHERE entry_id = old.id;
END;

