package bots

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/shyper/autodownloadarr/internal/access"
	"github.com/shyper/autodownloadarr/internal/auth"
	"github.com/shyper/autodownloadarr/internal/control"
	"github.com/shyper/autodownloadarr/internal/metadata/tmdb"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
)

func (s *Service) button(ctx context.Context, in Inbound, label string, a Action) (Button, error) {
	data, err := json.Marshal(a)
	if err != nil {
		return Button{}, err
	}
	token, err := s.Store.CreateBotAction(ctx, in.Provider, in.External, data)
	return Button{Label: clip(label, 70), Token: token, Danger: strings.HasPrefix(a.Op, "delete_"), Choice: a.Op == "show_movie" || a.Op == "show_series"}, err
}
func (s *Service) add(ctx context.Context, in Inbound, msg *Message, label string, a Action) error {
	b, err := s.button(ctx, in, label, a)
	if err != nil {
		return err
	}
	msg.Buttons = append(msg.Buttons, b)
	return nil
}
func (s *Service) failure(lang, code string) Message {
	texts := map[string][2]string{"forbidden": {"Du darfst nur eigene Anforderungen verwalten.", "You can only manage your own requests."}, "quota_exceeded": {"Deine Anforderungsquote ist ausgeschöpft.", "Your request quota is used up."}, "not_found": {"Dieser Eintrag existiert nicht mehr.", "This item no longer exists."}, "invalid_input": {"Bitte prüfe deine Eingabe.", "Please check your input."}, "not_configured": {"Ein benötigter Dienst ist noch nicht eingerichtet.", "A required service has not been configured."}, "expired": {"Dieser Button ist abgelaufen oder wurde bereits verwendet. Öffne die Ansicht erneut.", "This button expired or was already used. Open the view again."}, "operation_failed": {"Die Aktion ist fehlgeschlagen. Bitte versuche es erneut oder öffne die Weboberfläche.", "The operation failed. Try again or open the web interface."}}
	pair, ok := texts[code]
	if !ok {
		pair = texts["operation_failed"]
	}
	return Message{Title: text(lang, "Aktion nicht möglich", "Action unavailable"), Text: text(lang, pair[0], pair[1])}
}
func (s *Service) Render(ctx context.Context, in Inbound) (Message, error) {
	link, err := s.Store.BotLink(ctx, in.Provider, in.External)
	if in.Command == "connect" || err == store.ErrNotFound {
		if s.PublicURL == "" {
			return Message{Title: "AutoDownloadarr", Text: text(in.Language, "Die öffentliche Adresse ist noch nicht eingerichtet.", "The public URL is not configured yet.")}, nil
		}
		code, e := s.Store.CreateBotLinkCode(ctx, in.Provider, in.External, in.Chat, in.Language)
		if e != nil {
			return Message{}, e
		}
		return Message{Title: text(in.Language, "Konto verbinden", "Connect account"), Text: text(in.Language, "Melde dich in AutoDownloadarr an und bestätige die Verbindung. Der Link gilt 10 Minuten. Danach helfen /help und /search bei der Bedienung.", "Sign in to AutoDownloadarr and confirm the connection. The link expires in 10 minutes. Then use /help and /search to get started."), Buttons: []Button{{Label: text(in.Language, "Konto verbinden", "Connect account"), URL: s.PublicURL + "/connect-bot?code=" + code}}}, nil
	}
	if err != nil {
		return Message{}, err
	}
	in.Language = link.Language
	user, err := s.Auth.User(ctx, link.UserID)
	if err != nil {
		return s.failure(in.Language, "forbidden"), nil
	}
	a := Action{Op: in.Command, Query: in.Query}
	if in.Callback != "" {
		data, e := s.Store.TakeBotAction(ctx, in.Provider, in.External, in.Callback)
		if e != nil {
			return s.failure(in.Language, "expired"), nil
		}
		if json.Unmarshal(data, &a) != nil {
			return s.failure(in.Language, "invalid_input"), nil
		}
	}
	switch a.Op {
	case "search", "movie", "series":
		kind := "movie"
		if a.Op == "series" {
			kind = "series"
		}
		if strings.HasPrefix(a.Query, "m:") || strings.HasPrefix(a.Query, "s:") {
			id, e := strconv.Atoi(a.Query[2:])
			if e == nil {
				kind = "movie"
				if a.Query[0] == 's' {
					kind = "series"
				}
				return s.catalog(ctx, in, user, kind, id, control.Input{}, 0)
			}
		}
		if len(strings.TrimSpace(a.Query)) < 2 {
			return s.help(in.Language), nil
		}
		return s.search(ctx, in, user, kind, a.Query)
	case "library", "movies", "list_movies", "list_series":
		return s.library(ctx, in, user, a.Op == "list_series", a.Bool, a.Page)
	case "catalog_movie", "catalog_series":
		return s.catalog(ctx, in, user, strings.TrimPrefix(a.Op, "catalog_"), a.TMDBID, a.Options, a.Page)
	case "show_movie":
		return s.movie(ctx, in, user, a.ID)
	case "show_series":
		return s.show(ctx, in, user, a.ID)
	case "downloads":
		return s.downloads(ctx, in)
	case "language":
		lang := strings.ToLower(strings.TrimSpace(a.Query))
		if lang != "de" && lang != "en" {
			return Message{Title: text(in.Language, "Sprache", "Language"), Text: "/language de · /language en"}, nil
		}
		if err := s.Store.UpdateBotLink(ctx, link.UserID, link.ID, lang, link.Notifications); err != nil {
			return Message{}, err
		}
		return Message{Title: text(lang, "Sprache gespeichert", "Language saved")}, nil
	case "confirm_delete_movie", "confirm_forget_movie", "confirm_delete_series":
		kind := "movie"
		if strings.HasSuffix(a.Op, "series") {
			kind = "series"
		}
		if err := access.Check(ctx, s.Store, user, kind, a.ID); err != nil {
			return s.failure(in.Language, control.ErrorCode(err)), nil
		}
		msg := Message{Title: text(in.Language, "Löschen bestätigen", "Confirm deletion"), Text: text(in.Language, "Die verwalteten Dateien werden gelöscht. Fortfahren?", "The managed files will be deleted. Continue?")}
		op := "delete_" + kind
		input := control.Input{ID: a.ID, Forget: a.Op == "confirm_forget_movie"}
		if err := s.add(ctx, in, &msg, text(in.Language, "Ja, löschen", "Yes, delete"), Action{Op: op, ID: a.ID, Options: input}); err != nil {
			return Message{}, err
		}
		err = s.add(ctx, in, &msg, text(in.Language, "Abbrechen", "Cancel"), Action{Op: "show_" + kind, ID: a.ID})
		return msg, err
	case "window_menu":
		if err := access.Check(ctx, s.Store, user, "series", a.ID); err != nil {
			return s.failure(in.Language, control.ErrorCode(err)), nil
		}
		msg := Message{Title: text(in.Language, "Episodenfenster", "Episode window"), Text: text(in.Language, "Wie viele Episoden sollen bereitgehalten werden?", "How many episodes should be kept ready?")}
		for _, n := range []int{1, 2, 3, 5, 10} {
			if err := s.add(ctx, in, &msg, strconv.Itoa(n), Action{Op: "window_series", ID: a.ID, Value: n}); err != nil {
				return Message{}, err
			}
		}
		return msg, nil
	case "pause_series", "keep_series", "window_series":
		se, e := s.Store.Series(ctx, a.ID)
		if e != nil {
			return s.failure(in.Language, control.ErrorCode(e)), nil
		}
		settings := series.Settings{Seasons: se.Seasons, FutureSeasons: se.FutureSeasons, WindowSize: se.WindowSize, Trigger: se.DeleteTrigger, TriggerValue: se.TriggerValue, Keep: se.Keep, Paused: se.Paused}
		if a.Op == "pause_series" {
			settings.Paused = a.Bool
		}
		if a.Op == "keep_series" {
			settings.Keep = a.Bool
		}
		if a.Op == "window_series" {
			settings.WindowSize = a.Value
		}
		_, e = s.Control.Execute(ctx, user, "update_series", control.Input{ID: a.ID, Settings: &settings})
		if e != nil {
			return s.failure(in.Language, control.ErrorCode(e)), nil
		}
		return s.show(ctx, in, user, a.ID)
	case "request_movie", "request_series", "keep_movie", "search_movie", "search_episode", "delete_movie", "delete_series":
		input := a.Options
		input.ID = a.ID
		if a.TMDBID > 0 {
			input.TMDBID = a.TMDBID
		}
		if a.Op == "keep_movie" {
			input.Keep = a.Bool
		}
		result, e := s.Control.Execute(ctx, user, a.Op, input)
		s.Log.Info("bot operation", "provider", in.Provider, "user", user.ID, "operation", a.Op, "success", e == nil)
		if e != nil {
			return s.failure(in.Language, control.ErrorCode(e)), nil
		}
		if a.Op == "request_movie" {
			return s.movie(ctx, in, user, result.(store.Movie).ID)
		}
		if a.Op == "request_series" {
			return s.show(ctx, in, user, result.(store.Series).ID)
		}
		if a.Op == "keep_movie" || a.Op == "search_movie" {
			return s.movie(ctx, in, user, a.ID)
		}
		return Message{Title: text(in.Language, "Erledigt", "Done"), Text: text(in.Language, "Die Aktion wurde ausgeführt.", "The action completed.")}, nil
	default:
		return s.help(in.Language), nil
	}
}
func (s *Service) help(lang string) Message {
	return Message{Title: "AutoDownloadarr", Text: text(lang, "/search Titel – Filme suchen\n/series Titel – Serien suchen\n/library – Bibliothek\n/downloads – Downloads und Warteschlange\n/language de oder en – Sprache\n/connect – Konto verbinden\n\nDu kannst auch einen Filmtitel direkt als Nachricht senden.", "/search title – search movies\n/series title – search series\n/library – library\n/downloads – downloads and queue\n/language de or en – language\n/connect – connect account\n\nYou can also send a movie title as a message.")}
}
func (s *Service) search(ctx context.Context, in Inbound, u auth.User, kind, query string) (Message, error) {
	name := "search_movies"
	if kind == "series" {
		name = "search_series"
	}
	result, err := s.Control.Execute(ctx, u, name, control.Input{Query: query})
	if err != nil {
		return s.failure(in.Language, control.ErrorCode(err)), nil
	}
	msg := Message{Title: text(in.Language, "Suchergebnisse", "Search results"), Text: text(in.Language, "Wähle einen Titel für Details und Anforderungen.", "Choose a title for details and requests.")}
	if kind == "movie" {
		for i, m := range result.([]tmdb.Movie) {
			if i >= 5 {
				break
			}
			if i == 0 {
				msg.Poster = tmdb.PosterURL(m.PosterPath, "w342")
			}
			if err := s.add(ctx, in, &msg, fmt.Sprintf("%s (%d)", m.Title, m.Year), Action{Op: "catalog_movie", TMDBID: m.ID}); err != nil {
				return Message{}, err
			}
		}
	} else {
		for i, m := range result.([]tmdb.Show) {
			if i >= 5 {
				break
			}
			if i == 0 {
				msg.Poster = tmdb.PosterURL(m.PosterPath, "w342")
			}
			if err := s.add(ctx, in, &msg, fmt.Sprintf("%s (%d)", m.Title, m.Year), Action{Op: "catalog_series", TMDBID: m.ID}); err != nil {
				return Message{}, err
			}
		}
	}
	for i := range msg.Buttons {
		msg.Buttons[i].Choice = true
	}
	if len(msg.Buttons) == 0 {
		msg.Text = text(in.Language, "Keine Treffer gefunden.", "No matches found.")
	}
	return msg, nil
}
func (s *Service) catalog(ctx context.Context, in Inbound, u auth.User, kind string, id int, opts control.Input, page int) (Message, error) {
	name := "movie_catalog_details"
	if kind == "series" {
		name = "series_catalog_details"
	}
	result, err := s.Control.Execute(ctx, u, name, control.Input{TMDBID: id})
	if err != nil {
		return s.failure(in.Language, control.ErrorCode(err)), nil
	}
	msg := Message{}
	opts.TMDBID = id
	if kind == "movie" {
		m := result.(tmdb.Movie)
		msg.Title = fmt.Sprintf("%s (%d)", m.Title, m.Year)
		msg.Text = strings.Join(m.Genres, " · ") + "\n" + clip(m.Overview, 550)
		msg.Poster = tmdb.PosterURL(m.PosterPath, "w342")
		if old, e := s.Store.MovieByTMDB(ctx, id); e == nil {
			msg.Text += "\n" + state(in.Language, old.State)
			if err := s.add(ctx, in, &msg, text(in.Language, "Anforderung öffnen", "Open request"), Action{Op: "show_movie", ID: old.ID}); err != nil {
				return Message{}, err
			}
			if old.State != store.MovieDeleted {
				return msg, nil
			}
		}
		err = s.add(ctx, in, &msg, text(in.Language, "Film anfordern", "Request movie"), Action{Op: "request_movie", TMDBID: id, Options: opts})
		return msg, err
	}
	m := result.(tmdb.Show)
	msg.Title = fmt.Sprintf("%s (%d)", m.Title, m.Year)
	msg.Text = strings.Join(m.Genres, " · ") + "\n" + clip(m.Overview, 400)
	msg.Poster = tmdb.PosterURL(m.PosterPath, "w342")
	if old, e := s.Store.SeriesByTMDB(ctx, id); e == nil {
		err = s.add(ctx, in, &msg, text(in.Language, "Anforderung öffnen", "Open request"), Action{Op: "show_series", ID: old.ID})
		return msg, err
	}
	if len(opts.Options.Seasons) == 0 {
		opts.Options.FutureSeasons = true
	}
	if opts.Options.WindowSize == 0 {
		g, err := s.Store.General(ctx)
		if err != nil {
			return Message{}, err
		}
		opts.Options.WindowSize = max(g.SeriesWindow, 1)
	}
	msg.Text += "\n" + text(in.Language, "Staffeln auswählen (leer = alle). Episodenfenster: ", "Select seasons (empty = all). Episode window: ") + strconv.Itoa(opts.Options.WindowSize)
	if page < 0 {
		page = 0
	}
	start := min(page*5, len(m.Seasons))
	end := min(start+5, len(m.Seasons))
	for _, season := range m.Seasons[start:end] {
		if season.Number == 0 {
			continue
		}
		next := opts
		next.Options.Seasons = slices.Clone(opts.Options.Seasons)
		selected := len(next.Options.Seasons) == 0 || slices.Contains(next.Options.Seasons, season.Number)
		if len(next.Options.Seasons) == 0 {
			next.Options.Seasons = []int{season.Number}
		} else if index := slices.Index(next.Options.Seasons, season.Number); index >= 0 {
			next.Options.Seasons = slices.Delete(next.Options.Seasons, index, index+1)
		} else {
			next.Options.Seasons = append(next.Options.Seasons, season.Number)
		}
		label := fmt.Sprintf(text(in.Language, "Staffel %d", "Season %d"), season.Number)
		if selected {
			label = "✓ " + label
		}
		if err := s.add(ctx, in, &msg, label, Action{Op: "catalog_series", TMDBID: id, Options: next, Page: page}); err != nil {
			return Message{}, err
		}
	}
	if page > 0 {
		if err := s.add(ctx, in, &msg, "←", Action{Op: "catalog_series", TMDBID: id, Options: opts, Page: page - 1}); err != nil {
			return Message{}, err
		}
	}
	if end < len(m.Seasons) {
		if err := s.add(ctx, in, &msg, "→", Action{Op: "catalog_series", TMDBID: id, Options: opts, Page: page + 1}); err != nil {
			return Message{}, err
		}
	}
	all := opts
	all.Options.Seasons = nil
	all.Options.FutureSeasons = true
	if err := s.add(ctx, in, &msg, text(in.Language, "Alle Staffeln", "All seasons"), Action{Op: "catalog_series", TMDBID: id, Options: all, Page: page}); err != nil {
		return Message{}, err
	}
	future := opts
	future.Options.FutureSeasons = !opts.Options.FutureSeasons
	if !future.Options.FutureSeasons && len(future.Options.Seasons) == 0 {
		for _, season := range m.Seasons {
			if season.Number > 0 {
				future.Options.Seasons = append(future.Options.Seasons, season.Number)
			}
		}
	}
	if err := s.add(ctx, in, &msg, text(in.Language, "Künftige Staffeln: ", "Future seasons: ")+text(in.Language, map[bool]string{true: "Ja", false: "Nein"}[opts.Options.FutureSeasons], map[bool]string{true: "Yes", false: "No"}[opts.Options.FutureSeasons]), Action{Op: "catalog_series", TMDBID: id, Options: future, Page: page}); err != nil {
		return Message{}, err
	}
	for _, n := range []int{2, 3, 5} {
		next := opts
		next.Options.WindowSize = n
		if err := s.add(ctx, in, &msg, fmt.Sprintf(text(in.Language, "Fenster %d", "Window %d"), n), Action{Op: "catalog_series", TMDBID: id, Options: next, Page: page}); err != nil {
			return Message{}, err
		}
	}
	err = s.add(ctx, in, &msg, text(in.Language, "Serie anfordern", "Request series"), Action{Op: "request_series", TMDBID: id, Options: opts})
	return msg, err
}
func state(lang, value string) string {
	names := map[string][2]string{"available": {"Bereit zum Schauen", "Ready to watch"}, "downloading": {"Wird geladen", "Downloading"}, "queued": {"In Warteschlange", "Queued"}, "searching": {"Suche läuft", "Searching"}, "waiting": {"Wartet auf Quelle", "Waiting for a source"}, "watched": {"Gesehen", "Watched"}, "deleted": {"Gelöscht", "Deleted"}, "failed": {"Fehlgeschlagen", "Failed"}, "pending": {"Ausstehend", "Pending"}, "skipped": {"Übersprungen", "Skipped"}}
	if v, ok := names[value]; ok {
		return text(lang, v[0], v[1])
	}
	return value
}
func (s *Service) movie(ctx context.Context, in Inbound, u auth.User, id int64) (Message, error) {
	m, err := s.Store.Movie(ctx, id)
	if err != nil {
		return s.failure(in.Language, control.ErrorCode(err)), nil
	}
	msg := Message{Title: fmt.Sprintf("%s (%d)", m.Title, m.Year), Text: state(in.Language, m.State) + "\n" + strings.Join(m.Genres, " · ") + "\n" + clip(m.Overview, 400), Poster: tmdb.PosterURL(m.PosterPath, "w342")}
	if m.Progress > 0 {
		msg.Text += fmt.Sprintf("\n%d%%", int(m.Progress*100))
	}
	if m.DeleteAt != nil && !m.Keep {
		msg.Text += "\n" + text(in.Language, "Löschen: ", "Deletion: ") + m.DeleteAt.Format("2006-01-02 15:04 MST")
	}
	if access.CanManage(u, m.RequestedBy) {
		for _, button := range []struct {
			label string
			a     Action
		}{
			{text(in.Language, "Behalten umschalten", "Toggle keep"), Action{Op: "keep_movie", ID: id, Bool: !m.Keep}},
			{text(in.Language, "Dateien löschen", "Delete files"), Action{Op: "confirm_delete_movie", ID: id}},
			{text(in.Language, "Anforderung entfernen", "Remove request"), Action{Op: "confirm_forget_movie", ID: id}},
		} {
			if err := s.add(ctx, in, &msg, button.label, button.a); err != nil {
				return Message{}, err
			}
		}
		if m.State == store.MovieFailed || m.State == store.MovieWaiting {
			if err := s.add(ctx, in, &msg, text(in.Language, "Erneut suchen", "Search again"), Action{Op: "search_movie", ID: id}); err != nil {
				return Message{}, err
			}
		}
	}
	msg.Buttons = append(msg.Buttons, Button{Label: text(in.Language, "Details öffnen", "Open details"), URL: fmt.Sprintf("%s/movies/%d", s.PublicURL, id)})
	return msg, nil
}
func (s *Service) show(ctx context.Context, in Inbound, u auth.User, id int64) (Message, error) {
	se, err := s.Store.Series(ctx, id)
	if err != nil {
		return s.failure(in.Language, control.ErrorCode(err)), nil
	}
	eps, err := s.Store.Episodes(ctx, id)
	if err != nil {
		return Message{}, err
	}
	status := se.Status
	names := map[string][2]string{"Returning Series": {"Fortlaufend", "Ongoing"}, "Ended": {"Beendet", "Ended"}, "Canceled": {"Abgesetzt", "Canceled"}, "In Production": {"In Produktion", "In production"}, "Planned": {"Geplant", "Planned"}, "Pilot": {"Pilotfolge", "Pilot"}}
	if pair, ok := names[status]; ok {
		status = text(in.Language, pair[0], pair[1])
	}
	msg := Message{Title: se.Title, Text: status + "\n" + strings.Join(se.Genres, " · ") + "\n" + fmt.Sprintf(text(in.Language, "Episodenfenster: %d", "Episode window: %d"), se.WindowSize), Poster: tmdb.PosterURL(se.PosterPath, "w342")}
	if se.Paused {
		msg.Text += "\n" + text(in.Language, "Pausiert", "Paused")
	}
	if se.Keep {
		msg.Text += "\n" + text(in.Language, "Wird behalten", "Kept")
	}
	count := 0
	for _, ep := range eps {
		if ep.State == store.EpisodePending || ep.State == store.EpisodeSkipped || ep.State == store.MovieDeleted {
			continue
		}
		msg.Text += fmt.Sprintf("\nS%02dE%02d · %s", ep.Season, ep.Episode, state(in.Language, ep.State))
		count++
		if count >= 5 {
			break
		}
	}
	if access.CanManage(u, se.RequestedBy) {
		for _, b := range []struct {
			label string
			a     Action
		}{
			{text(in.Language, "Pause umschalten", "Toggle pause"), Action{Op: "pause_series", ID: id, Bool: !se.Paused}},
			{text(in.Language, "Behalten umschalten", "Toggle keep"), Action{Op: "keep_series", ID: id, Bool: !se.Keep}},
			{text(in.Language, "Episodenfenster", "Episode window"), Action{Op: "window_menu", ID: id}},
			{text(in.Language, "Serie löschen", "Delete series"), Action{Op: "confirm_delete_series", ID: id}},
		} {
			if err := s.add(ctx, in, &msg, b.label, b.a); err != nil {
				return Message{}, err
			}
		}
	}
	msg.Buttons = append(msg.Buttons, Button{Label: text(in.Language, "Details öffnen", "Open details"), URL: fmt.Sprintf("%s/series/%d", s.PublicURL, id)})
	return msg, nil
}
func (s *Service) library(ctx context.Context, in Inbound, u auth.User, tv, all bool, page int) (Message, error) {
	msg := Message{Title: text(in.Language, "Bibliothek", "Library"), Text: text(in.Language, "Wähle eine Anforderung.", "Choose a request.")}
	n := 0
	if page < 0 {
		page = 0
	}
	start := page * 10
	seen := 0
	more := false
	if tv {
		list, err := s.Store.SeriesList(ctx)
		if err != nil {
			return Message{}, err
		}
		for _, m := range list {
			if !all && m.RequestedBy != u.ID {
				continue
			}
			if seen < start {
				seen++
				continue
			}
			if n >= 10 {
				more = true
				break
			}
			n++
			if err := s.add(ctx, in, &msg, m.Title, Action{Op: "show_series", ID: m.ID}); err != nil {
				return Message{}, err
			}
		}
	} else {
		list, err := s.Store.Movies(ctx)
		if err != nil {
			return Message{}, err
		}
		for _, m := range list {
			if !all && m.RequestedBy != u.ID {
				continue
			}
			if seen < start {
				seen++
				continue
			}
			if n >= 10 {
				more = true
				break
			}
			n++
			if err := s.add(ctx, in, &msg, m.Title+" · "+state(in.Language, m.State), Action{Op: "show_movie", ID: m.ID}); err != nil {
				return Message{}, err
			}
		}
	}
	if n == 0 {
		msg.Text = text(in.Language, "Keine Anforderungen vorhanden.", "No requests yet.")
	}
	if err := s.add(ctx, in, &msg, text(in.Language, "Filme", "Movies"), Action{Op: "list_movies", Bool: all}); err != nil {
		return Message{}, err
	}
	if err := s.add(ctx, in, &msg, text(in.Language, "Serien", "Series"), Action{Op: "list_series", Bool: all}); err != nil {
		return Message{}, err
	}
	pageOp := "list_movies"
	if tv {
		pageOp = "list_series"
	}
	if page > 0 {
		if err := s.add(ctx, in, &msg, "←", Action{Op: pageOp, Bool: all, Page: page - 1}); err != nil {
			return Message{}, err
		}
	}
	if more {
		if err := s.add(ctx, in, &msg, "→", Action{Op: pageOp, Bool: all, Page: page + 1}); err != nil {
			return Message{}, err
		}
	}
	label := text(in.Language, "Gemeinsame Bibliothek", "Shared library")
	if all {
		label = text(in.Language, "Meine Anforderungen", "My requests")
	}
	op := "list_movies"
	if tv {
		op = "list_series"
	}
	err := s.add(ctx, in, &msg, label, Action{Op: op, Bool: !all})
	return msg, err
}
func (s *Service) downloads(ctx context.Context, in Inbound) (Message, error) {
	msg := Message{Title: text(in.Language, "Downloads", "Downloads")}
	for _, d := range s.Control.Movies.Downloads() {
		m, err := s.Store.Movie(ctx, d.MovieID)
		if err != nil {
			continue
		}
		percent := 0
		if d.Progress.TotalBytes > 0 {
			percent = int(100 * d.Progress.DoneBytes / d.Progress.TotalBytes)
		}
		msg.Text += fmt.Sprintf("%s · %d%%\n", m.Title, percent)
	}
	for _, d := range s.Control.Series.Downloads() {
		se, err := s.Store.Series(ctx, d.SeriesID)
		if err != nil {
			continue
		}
		percent := 0
		if d.Progress.TotalBytes > 0 {
			percent = int(100 * d.Progress.DoneBytes / d.Progress.TotalBytes)
		}
		msg.Text += fmt.Sprintf("%s S%02dE%02d · %d%%\n", se.Title, d.Season, d.Episode, percent)
	}
	queue, err := s.Store.Queue(ctx)
	if err != nil {
		return Message{}, err
	}
	msg.Text += "\n" + text(in.Language, "Warteschlange", "Queue")
	for i, q := range queue {
		if i >= 8 {
			break
		}
		msg.Text += "\n" + q.Label
	}
	if len(queue) == 0 {
		msg.Text += " · " + text(in.Language, "leer", "empty")
	}
	err = s.add(ctx, in, &msg, text(in.Language, "Aktualisieren", "Refresh"), Action{Op: "downloads"})
	return msg, err
}
func (s *Service) autocomplete(ctx context.Context, u auth.User, in Inbound) []map[string]string {
	result := []map[string]string{}
	kind := "search_movies"
	if in.Command == "series" {
		kind = "search_series"
	}
	data, err := s.Control.Execute(ctx, u, kind, control.Input{Query: in.Query})
	if err != nil {
		return result
	}
	if kind == "search_movies" {
		for i, m := range data.([]tmdb.Movie) {
			if i >= 10 {
				break
			}
			result = append(result, map[string]string{"name": clip(fmt.Sprintf("%s (%d)", m.Title, m.Year), 100), "value": "m:" + strconv.Itoa(m.ID)})
		}
	} else {
		for i, m := range data.([]tmdb.Show) {
			if i >= 10 {
				break
			}
			result = append(result, map[string]string{"name": clip(fmt.Sprintf("%s (%d)", m.Title, m.Year), 100), "value": "s:" + strconv.Itoa(m.ID)})
		}
	}
	return result
}
