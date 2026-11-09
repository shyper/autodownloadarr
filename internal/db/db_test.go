package db

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestOpenMigratesAndIsReopenable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, table := range []string{"users", "sessions", "scheduled_tasks"} {
		var name string
		err := first.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
	var fk int
	if err := first.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d (err %v), want 1", fk, err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = second.Close()
}

func TestLegacyRequestOwnershipMigration(t *testing.T) {
	ctx := t.Context()
	d, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, d, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(3,'first','unused','admin',0,0),(7,'second','unused','user',1,1)`,
		`INSERT INTO movies(tmdb_id,title,state,requested_by,created_at,updated_at) VALUES(1,'Legacy','available',NULL,0,0),(2,'Owned','available',7,0,0)`,
		`INSERT INTO series(tmdb_id,title,requested_by,created_at,updated_at) VALUES(1,'Legacy',NULL,0,0),(2,'Owned',7,0,0)`,
	} {
		if _, err = d.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err = migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"movies", "series"} {
		for id, want := range map[int]int{1: 3, 2: 7} {
			var got int
			if err = d.QueryRowContext(ctx, `SELECT requested_by FROM `+table+` WHERE tmdb_id=?`, id).Scan(&got); err != nil || got != want {
				t.Fatalf("%s owner %d=%d want %d (%v)", table, id, got, want, err)
			}
		}
	}
}

func TestReleaseFingerprintMigration(t *testing.T) {
	ctx := t.Context()
	d, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "fingerprints.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, d, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO movies(id,tmdb_id,title,state,release_key,created_at,updated_at) VALUES(1,1,'Movie','available','0:guid:movie-upload',0,0)`,
		`INSERT INTO series(id,tmdb_id,title,created_at,updated_at) VALUES(1,2,'Show',0,0)`,
		`INSERT INTO episodes(id,series_id,season,episode,state,release_key,created_at,updated_at) VALUES(1,1,1,1,'available','0:fp:episode-fingerprint',0,0)`,
		`INSERT INTO releases(movie_id,title,guid,fingerprint,size,nzb_url,searched_at) VALUES(1,'Movie','movie-upload','movie-fingerprint',1,'',0)`,
		`INSERT INTO releases(episode_id,title,guid,fingerprint,size,nzb_url,searched_at) VALUES(1,'Episode','','episode-fingerprint',1,'',0)`,
	} {
		if _, err = d.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err = migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]string{"movies": "movie-fingerprint", "episodes": "episode-fingerprint"} {
		var got string
		if err = d.QueryRowContext(ctx, "SELECT release_fingerprint FROM "+table+" WHERE id=1").Scan(&got); err != nil || got != want {
			t.Fatalf("%s fingerprint %q, want %q: %v", table, got, want, err)
		}
	}
}
