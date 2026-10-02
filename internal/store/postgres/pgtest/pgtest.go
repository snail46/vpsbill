// Package pgtest gives a test package its own database, so integration
// tests of different packages can run at the same time.
package pgtest

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"vpsbill/internal/store/postgres"
)

// Open returns an empty, migrated database named after TEST_DATABASE_URL's
// with _<name> added, on the same server. It skips the test when
// TEST_DATABASE_URL is not set.
func Open(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if !strings.Contains(database, "vpsbill_test") {
		t.Fatal("refusing to create test databases without vpsbill_test in TEST_DATABASE_URL")
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && r != '_' {
			t.Fatalf("database suffix %q must be lower-case letters", name)
		}
	}
	own := database + "_" + name
	ctx := context.Background()
	admin, err := postgres.Open(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	// CREATE DATABASE cannot run in a transaction; each statement is its own.
	if _, err = admin.Exec(ctx, `DROP DATABASE IF EXISTS "`+own+`" WITH (FORCE)`); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `CREATE DATABASE "`+own+`"`); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	admin.Close()
	parsed.Path = "/" + own
	db, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err = postgres.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}
