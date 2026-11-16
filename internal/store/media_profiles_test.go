package store

import (
	"context"
	"errors"
	"testing"
)

func TestMediaProfiles(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	for id, name := range map[int64]string{1: "alice", 2: "bob"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,role,created_at,updated_at) VALUES(?,?,'x','user',0,0)`, id, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SyncMediaProfiles(ctx, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sync without server = %v", err)
	}
	if _, err := s.SaveMediaServer(ctx, MediaServer{Kind: "plex", URL: "http://plex", Token: "t", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncMediaProfiles(ctx, []MediaProfileInfo{
		{ExternalID: "owner", Name: "Owner", Owner: true},
		{ExternalID: "kid", Name: "Kid", Protected: true},
		{ExternalID: "guest", Name: "Guest"},
	}); err != nil {
		t.Fatal(err)
	}
	list, err := s.MediaProfiles(ctx)
	if err != nil || len(list) != 3 || !list[0].Owner {
		t.Fatalf("profiles = %+v, %v", list, err)
	}
	var kid MediaProfile
	for _, p := range list {
		if p.ExternalID == "kid" {
			kid = p
		}
	}
	if !kid.Protected || kid.Name != "Kid" {
		t.Fatalf("kid = %+v", kid)
	}
	if err := s.AssignMediaProfile(ctx, kid.ID, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMediaProfileTokens(ctx, kid.ID, "server", "account"); err != nil {
		t.Fatal(err)
	}
	got, err := s.MediaProfileForUser(ctx, 2)
	if err != nil || got.ID != kid.ID || got.UserName != "bob" || !got.Watchlist || got.ServerToken != "server" || got.AccountToken != "account" || !got.HasToken {
		t.Fatalf("profile of bob = %+v, %v", got, err)
	}
	if got.AccountKey() != ProfileAccountKey(kid.ID) || ProfileIDFromAccountKey(got.AccountKey()) != kid.ID || ProfileIDFromAccountKey("") != 0 {
		t.Fatal("account key round trip")
	}
	// A user has one profile at most.
	if err := s.AssignMediaProfile(ctx, list[0].ID, 2, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("second profile for bob = %v", err)
	}
	// A new sync keeps the assignment and tokens, and drops vanished profiles.
	if err := s.SyncMediaProfiles(ctx, []MediaProfileInfo{{ExternalID: "owner", Name: "Owner", Owner: true}, {ExternalID: kid.ExternalID, Name: "Renamed"}}); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.MediaProfiles(ctx); len(list) != 2 {
		t.Fatalf("profiles after sync = %d", len(list))
	}
	if got, err = s.MediaProfileForUser(ctx, 2); err != nil || got.Name != "Renamed" || got.ServerToken != "server" {
		t.Fatalf("after sync = %+v, %v", got, err)
	}
	if err := s.SetMediaProfileError(ctx, kid.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.MediaProfile(ctx, kid.ID); got.LastError != "boom" {
		t.Fatalf("error = %q", got.LastError)
	}
	// Deleting the user removes the assignment.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.MediaProfile(ctx, kid.ID); got.UserID != 0 {
		t.Fatal("assignment survived the user")
	}
	if _, err := s.MediaProfileForUser(ctx, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("profile of deleted user = %v", err)
	}
	// Profiles belong to the server software they were read from.
	if _, err := s.SaveMediaServer(ctx, MediaServer{Kind: "jellyfin", URL: "http://jf", Token: "k", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.MediaProfiles(ctx); len(list) != 0 {
		t.Fatalf("profiles after switching to Jellyfin = %d", len(list))
	}
}

func TestWatchBaseAccountPersists(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	mv, err := s.CreateMovie(ctx, Movie{TMDBID: 1, Title: "Film", State: MovieAvailable}, 0)
	if err != nil {
		t.Fatal(err)
	}
	mv.Base = WatchBase{Set: true, Views: 2, Account: "profile:3"}
	if err := s.UpdateMovie(ctx, mv); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Movie(ctx, mv.ID); got.Base != mv.Base {
		t.Fatalf("base = %+v", got.Base)
	}
}
