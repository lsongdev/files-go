package database

import (
	"context"
	"testing"
	"time"
)

func TestWALAllowsReadsDuringWriteTransaction(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	db, err := Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reader, err := OpenReader(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE concurrency_test (value INTEGER); INSERT INTO concurrency_test VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE concurrency_test SET value=2`); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var value int
	if err := reader.QueryRowContext(readCtx, `SELECT value FROM concurrency_test`).Scan(&value); err != nil {
		t.Fatalf("read was blocked by writer: %v", err)
	}
	if value != 1 {
		t.Fatalf("uncommitted value became visible: %d", value)
	}
}
