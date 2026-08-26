package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// seedDocument inserts a minimal resource_sets/documents row pair so that a
// chunks row (which foreign_keys=ON requires a document_id for) can be
// inserted. Returns the new document's id.
func seedDocument(t *testing.T, db *sql.DB) int64 {
	t.Helper()

	res, err := db.Exec(
		`INSERT INTO resource_sets (name, seq, indexed_at) VALUES (?, ?, ?)`,
		"test-set", 1, "2026-08-26T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("seedDocument: insert resource_sets: %v", err)
	}
	setID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seedDocument: resource_sets LastInsertId: %v", err)
	}

	res, err = db.Exec(
		`INSERT INTO documents (set_id, rel_path, doc_kind, content_sha256, size_bytes, indexed_at)
         VALUES (?, ?, ?, ?, ?, ?)`,
		setID, "doc.md", "markdown", "deadbeef", 10, "2026-08-26T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("seedDocument: insert documents: %v", err)
	}
	docID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seedDocument: documents LastInsertId: %v", err)
	}
	return docID
}

// TestStore_FTS5AvailableCGOFree verifies REQ-06 / S-23: the modernc.org/sqlite
// driver exposes a working FTS5 virtual table and bm25() query on the host
// this test runs on. This is the test S-23's verification command runs
// inside golang:1.25-alpine (musl, CGO_ENABLED=0) — a darwin pass here is
// NOT evidence for the alpine claim, only for the driver working at all.
func TestStore_FTS5AvailableCGOFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	// chunks_fts must exist as an FTS5 virtual table.
	var sqlText string
	if err := store.db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE name = 'chunks_fts'`,
	).Scan(&sqlText); err != nil {
		t.Fatalf("query sqlite_master for chunks_fts: %v", err)
	}
	if !containsFTS5(sqlText) {
		t.Fatalf("chunks_fts: want an fts5 virtual table definition, got %q", sqlText)
	}

	docID := seedDocument(t, store.db)
	if _, err := store.db.Exec(
		`INSERT INTO chunks (document_id, ord, heading, text, start_line, end_line, token_est, indexed_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		docID, 1, "Setup", "install the widget with the provided installer script", 1, 3, 8, "2026-08-26T00:00:00Z",
	); err != nil {
		t.Fatalf("insert chunks: %v", err)
	}

	rows, err := store.db.Query(
		`SELECT rowid, bm25(chunks_fts) FROM chunks_fts WHERE chunks_fts MATCH ? ORDER BY bm25(chunks_fts)`,
		"widget",
	)
	if err != nil {
		t.Fatalf("bm25 query: %v", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var rowID int64
		var score float64
		if err := rows.Scan(&rowID, &score); err != nil {
			t.Fatalf("scan bm25 row: %v", err)
		}
		found = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("bm25 rows iteration: %v", err)
	}
	if !found {
		t.Fatal("bm25 query: want at least one ranked result, got none")
	}
}

// containsFTS5 reports whether a sqlite_master CREATE VIRTUAL TABLE
// definition names the fts5 module.
func containsFTS5(sqlText string) bool {
	for i := 0; i+4 <= len(sqlText); i++ {
		if sqlText[i:i+4] == "fts5" {
			return true
		}
	}
	return false
}

// TestStore_FTSTriggersStayInSync verifies the T02 external-content FTS5
// sync triggers (chunks_ai/chunks_ad/chunks_au): external-content FTS5 does
// not auto-sync with its content table, so insert/update/delete on chunks
// must each be mirrored into chunks_fts by trigger, not by the engine.
func TestStore_FTSTriggersStayInSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	docID := seedDocument(t, store.db)

	// Insert: the row must be findable via chunks_fts MATCH.
	res, err := store.db.Exec(
		`INSERT INTO chunks (document_id, ord, heading, text, start_line, end_line, token_est, indexed_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		docID, 1, "Setup", "the quick alpaca jumps over the fence", 1, 3, 8, "2026-08-26T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("insert chunks: %v", err)
	}
	chunkID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("chunks LastInsertId: %v", err)
	}

	if !ftsMatches(t, store.db, "alpaca") {
		t.Fatal("after insert: chunks_fts MATCH 'alpaca' found no rows, want the chunks_ai trigger to have synced it")
	}

	// Update: the old term must stop matching, the new term must match.
	if _, err := store.db.Exec(
		`UPDATE chunks SET text = ? WHERE id = ?`,
		"the quick zebra jumps over the fence", chunkID,
	); err != nil {
		t.Fatalf("update chunks: %v", err)
	}
	if ftsMatches(t, store.db, "alpaca") {
		t.Fatal("after update: chunks_fts MATCH 'alpaca' still found a row, want the chunks_au trigger to have removed the old term")
	}
	if !ftsMatches(t, store.db, "zebra") {
		t.Fatal("after update: chunks_fts MATCH 'zebra' found no rows, want the chunks_au trigger to have synced the new text")
	}

	// Delete: the row must no longer be findable at all.
	if _, err := store.db.Exec(`DELETE FROM chunks WHERE id = ?`, chunkID); err != nil {
		t.Fatalf("delete chunks: %v", err)
	}
	if ftsMatches(t, store.db, "zebra") {
		t.Fatal("after delete: chunks_fts MATCH 'zebra' still found a row, want the chunks_ad trigger to have removed it")
	}
}

// ftsMatches reports whether chunks_fts has any row matching term.
func ftsMatches(t *testing.T, db *sql.DB, term string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(
		`SELECT count(*) FROM chunks_fts WHERE chunks_fts MATCH ?`, term,
	).Scan(&count); err != nil {
		t.Fatalf("ftsMatches(%q): %v", term, err)
	}
	return count > 0
}

// TestStore_OpenIsIdempotent verifies that Open()ing the same store path
// twice succeeds both times, and the second Open does not duplicate the
// singleton schema_meta row nor overwrite its built_at.
func TestStore_OpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	var builtAtFirst string
	if err := first.db.QueryRow(
		`SELECT built_at FROM schema_meta WHERE id = 1`,
	).Scan(&builtAtFirst); err != nil {
		t.Fatalf("read built_at after first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first Store: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()

	var rowCount int
	if err := second.db.QueryRow(
		`SELECT count(*) FROM schema_meta`,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count schema_meta rows after second Open: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("schema_meta row count: want 1, got %d — second Open() must not duplicate it", rowCount)
	}

	var builtAtSecond string
	if err := second.db.QueryRow(
		`SELECT built_at FROM schema_meta WHERE id = 1`,
	).Scan(&builtAtSecond); err != nil {
		t.Fatalf("read built_at after second Open: %v", err)
	}
	if builtAtSecond != builtAtFirst {
		t.Errorf("built_at: want unchanged %q, got %q — second Open() must not overwrite it", builtAtFirst, builtAtSecond)
	}
}

// TestStore_SchemaVersionReadable verifies that a freshly opened store
// carries the SchemaVersion constant in schema_meta.schema_version, and
// that it can be read back.
func TestStore_SchemaVersionReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var version int
	if err := store.db.QueryRow(
		`SELECT schema_version FROM schema_meta WHERE id = 1`,
	).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("schema_version: want %d (SchemaVersion), got %d", SchemaVersion, version)
	}
}
