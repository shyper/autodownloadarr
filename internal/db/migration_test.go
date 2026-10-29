package db

import (
	"context"
	"database/sql"
	"io/fs"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMediaServerMigrationsUpgrade checks the table rebuild that allows
// Jellyfin/Emby on a database from before that change, including the path
// mappings' foreign key.
func TestMediaServerMigrationsUpgrade(t *testing.T) {
	ctx := context.Background()
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	dsn := "file:" + filepath.Join(t.TempDir(), "old.db") + "?" + q.Encode()
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	sqldb.SetMaxOpenConns(1)

	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, sqldb, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.ExecContext(ctx, `INSERT INTO media_servers
		(kind, name, url, token_enc, movie_library, tv_library, enabled, created_at, updated_at)
		VALUES ('plex', 'Plex', 'http://plex:32400', '', '1', '2', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.ExecContext(ctx, `INSERT INTO path_mappings (media_server_id, local_path, remote_path)
		VALUES (1, '/movies', '/data/movies')`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(ctx, sqldb); err != nil {
		t.Fatal(err)
	}
	if _, err := sqldb.ExecContext(ctx, `UPDATE media_servers SET kind = 'jellyfin' WHERE id = 1`); err != nil {
		t.Fatalf("jellyfin not accepted: %v", err)
	}
	if _, err := sqldb.ExecContext(ctx, `UPDATE media_servers SET kind = 'unknown' WHERE id = 1`); err == nil {
		t.Error("unknown kind accepted")
	}
	var account string
	if err := sqldb.QueryRowContext(ctx, `SELECT account_token_enc FROM media_servers WHERE id = 1`).Scan(&account); err != nil {
		t.Errorf("account token column missing: %v", err)
	}
	if _, err := sqldb.ExecContext(ctx, `DELETE FROM media_servers WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := sqldb.QueryRowContext(ctx, `SELECT COUNT(*) FROM path_mappings`).Scan(&n); err != nil || n != 0 {
		t.Errorf("path mappings after delete = %d, %v", n, err)
	}
}
