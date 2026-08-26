PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS resource_sets (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    seq INTEGER NOT NULL,
    indexed_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tags (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS set_tags (
    set_id INTEGER NOT NULL REFERENCES resource_sets(id),
    tag_id INTEGER NOT NULL REFERENCES tags(id),
    PRIMARY KEY (set_id, tag_id)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS documents (
    id INTEGER PRIMARY KEY,
    set_id INTEGER NOT NULL REFERENCES resource_sets(id) ON DELETE CASCADE,
    rel_path TEXT NOT NULL,
    doc_kind TEXT NOT NULL,
    content_sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    indexed_at TEXT NOT NULL,
    UNIQUE (set_id, rel_path)
);

CREATE TABLE IF NOT EXISTS chunks (
    -- id doubles as chunks_fts.rowid
    id INTEGER PRIMARY KEY,
    document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    ord INTEGER NOT NULL,
    heading TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    token_est INTEGER NOT NULL,
    indexed_at TEXT NOT NULL,
    UNIQUE (document_id, ord)
);

CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    text,
    heading,
    content='chunks',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, text, heading) VALUES (new.id, new.text, new.heading);
END;

CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text, heading) VALUES ('delete', old.id, old.text, old.heading);
END;

CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text, heading) VALUES ('delete', old.id, old.text, old.heading);
    INSERT INTO chunks_fts(rowid, text, heading) VALUES (new.id, new.text, new.heading);
END;

CREATE TABLE IF NOT EXISTS embeddings (
    chunk_id INTEGER PRIMARY KEY REFERENCES chunks(id) ON DELETE CASCADE,
    dim INTEGER NOT NULL,
    vec BLOB NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_meta (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    schema_version INTEGER NOT NULL,
    tool_version TEXT NOT NULL,
    built_at TEXT NOT NULL,
    resources_sha256 TEXT NOT NULL,
    embed_model TEXT NOT NULL,
    embed_dim INTEGER NOT NULL,
    chunk_count INTEGER NOT NULL
);
