package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func traktFixture(t *testing.T, s *Store) {
	t.Helper()
	for _, id := range []int64{1, 2} {
		if _, err := s.db.ExecContext(t.Context(), `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(?,?,?,'user',0,0)`, id, []string{"", "one", "two"}[id], "hash"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(t.Context(), `INSERT INTO sessions(id,token_hash,user_id,created_at,expires_at,last_seen_at) VALUES(?,?,?,0,9999999999999,0)`, id, []byte{byte(id)}, id); err != nil {
			t.Fatal(err)
		}
	}
}
func TestTraktTokensSettingsAndUserIsolation(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	traktFixture(t, s)
	link := TraktLink{UserID: 1, ClientID: "client", AccountID: "account", Username: "one", AccessToken: "private-access", RefreshToken: "private-refresh", ExpiresAt: 99}
	if err := s.SaveTraktLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTraktSettings(ctx, 1, TraktSettings{Enabled: true, Watchlist: true, ListIDs: []int64{12, 12}, PollMinutes: 17}); err != nil {
		t.Fatal(err)
	}
	got, err := s.TraktLink(ctx, 1)
	if err != nil || got.PollMinutes != 17 || len(got.ListIDs) != 1 || got.AccessToken != link.AccessToken {
		t.Fatalf("link: %+v %v", got, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "private-") {
		t.Fatal("credentials serialized")
	}
	var access, refresh string
	if err := s.db.QueryRowContext(ctx, `SELECT access_enc,refresh_enc FROM trakt_links WHERE user_id=1`).Scan(&access, &refresh); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(access, "private-access") || strings.Contains(refresh, "private-refresh") {
		t.Fatal("credentials not encrypted")
	}
	if _, err := s.TraktLink(ctx, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal("another user sees the connection")
	}
	// Rotation persists both tokens together and retains the user's interval and lists.
	link.AccessToken, link.RefreshToken = "rotated-access", "rotated-refresh"
	if err := s.SaveTraktLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.db, s.box)
	got, err = restarted.TraktLink(ctx, 1)
	if err != nil || got.RefreshToken != "rotated-refresh" || got.PollMinutes != 17 || !got.Watchlist {
		t.Fatal("rotation lost credentials or settings")
	}
	link.AccountID = "different-account"
	if err := s.SaveTraktLink(ctx, link); err != nil {
		t.Fatal(err)
	}
	got, _ = s.TraktLink(ctx, 1)
	if got.Watchlist || len(got.ListIDs) != 0 {
		t.Fatal("imported the previous account's sources")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TraktLink(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("user deletion retained credentials")
	}
}
func TestTraktOAuthStateIsSessionBoundSingleUseAndExpiring(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	traktFixture(t, s)
	if err := s.CreateTraktState(ctx, 1, 1, "client", "state", "private-verifier"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, session int64
		client        string
	}{{2, 2, "client"}, {1, 2, "client"}, {1, 1, "other-client"}} {
		if _, err := s.ConsumeTraktState(ctx, tc.user, tc.session, tc.client, "state"); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted mismatched OAuth state")
		}
	}
	got, err := s.ConsumeTraktState(ctx, 1, 1, "client", "state")
	if err != nil || got != "private-verifier" {
		t.Fatal("correct OAuth state rejected")
	}
	if _, err := s.ConsumeTraktState(ctx, 1, 1, "client", "state"); !errors.Is(err, ErrInvalid) {
		t.Fatal("OAuth replay accepted")
	}
	if err := s.CreateTraktState(ctx, 1, 1, "client", "expired", "verifier"); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if _, err := s.ConsumeTraktState(ctx, 1, 1, "client", "expired"); !errors.Is(err, ErrInvalid) {
		t.Fatal("expired OAuth state accepted")
	}
}
func TestManualSeriesRemovalExcludesAutomaticImports(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	se, err := s.CreateSeries(ctx, Series{TMDBID: 55, Title: "Show", WindowSize: 2}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSeries(ctx, se.ID); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.db, s.box)
	if excluded, err := restarted.AutomaticImportExcluded(ctx, "show", 55); err != nil || !excluded {
		t.Fatal("manual removal not remembered")
	}
	if _, err := s.CreateSeries(ctx, Series{TMDBID: 55, Title: "Show", WindowSize: 2}, 0); err != nil {
		t.Fatal("explicit requests were blocked")
	}
}
