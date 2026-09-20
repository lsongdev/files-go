package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/glebarez/go-sqlite"
	"github.com/lsongdev/files-go/migrations"
)

func Open(ctx context.Context, dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "files.db")
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", dbPath))
	if err != nil {
		return nil, err
	}
	// All mutations share this handle. BEGIN IMMEDIATE plus one pooled
	// connection gives the process a strict single-writer queue.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrations.Apply(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA optimize`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// OpenReader returns a separate read pool so long-running scans and queued
// writes cannot starve API/catalog reads. Open must be called first so the
// database and migrations already exist.
func OpenReader(ctx context.Context, dataDir string) (*sql.DB, error) {
	dbPath := filepath.Join(dataDir, "catalog.db")
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(ON)&_pragma=busy_timeout(5000)", dbPath))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
