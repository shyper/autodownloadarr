package store

import (
	"errors"
	"strings"
	"testing"
)

func TestPushoverSecretsAndProviderSwitch(t *testing.T) {
	s, ctx := newStore(t), t.Context()
	n := Notification{Enabled: true, Provider: "pushover", Token: "private-app", UserKey: "private-user", Device: "phone"}
	if err := s.SetNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	got, err := s.Notification(ctx)
	if err != nil || got.Token != n.Token || got.UserKey != n.UserKey || !got.HasToken || !got.HasUserKey || got.Device != "phone" {
		t.Fatalf("round trip failed: %v", err)
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='notifications'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, n.Token) || strings.Contains(raw, n.UserKey) {
		t.Fatal("Pushover credentials stored in plain text")
	}
	got.Token, got.UserKey = "", ""
	got.Device = "tablet"
	if err := s.SetNotification(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, err := s.Notification(ctx)
	if err != nil || again.Token != n.Token || again.UserKey != n.UserKey || again.Device != "tablet" {
		t.Fatal("empty secret fields did not preserve credentials")
	}
	if err := s.SetNotification(ctx, Notification{Provider: "telegram", ChatID: "42"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reused Pushover token as Telegram credential: %v", err)
	}
	if err := s.SetNotification(ctx, Notification{Provider: "telegram", ChatID: "42", Token: "telegram-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotification(ctx, Notification{Provider: "pushover", UserKey: "user"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reused Telegram token as Pushover credential: %v", err)
	}
}

func TestEpisodeArtworkRefreshPreservesState(t *testing.T) {
	s, ctx := newStore(t), t.Context()
	se, err := s.CreateSeries(ctx, Series{TMDBID: 45, Title: "Show", WindowSize: 2}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ep := Episode{Season: 2, Episode: 5, Title: "Episode", StillPath: "/still.jpg", SeasonPosterPath: "/season.jpg"}
	if err := s.UpsertEpisodes(ctx, se.ID, []Episode{ep}); err != nil {
		t.Fatal(err)
	}
	list, err := s.Episodes(ctx, se.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("episodes: %v", err)
	}
	got := list[0]
	if got.StillPath != ep.StillPath || got.SeasonPosterPath != ep.SeasonPosterPath {
		t.Fatal("artwork lost on insert")
	}
	got.State = MovieAvailable
	got.ReleaseTitle = "release"
	if err := s.UpdateEpisode(ctx, got); err != nil {
		t.Fatal(err)
	}
	ep.StillPath = ""
	ep.SeasonPosterPath = "/new-season.jpg"
	if err := s.UpsertEpisodes(ctx, se.ID, []Episode{ep}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Episode(ctx, got.ID)
	if err != nil || got.State != MovieAvailable || got.ReleaseTitle != "release" || got.StillPath != "" || got.SeasonPosterPath != "/new-season.jpg" {
		t.Fatalf("artwork refresh changed state or failed: %+v %v", got, err)
	}
}
