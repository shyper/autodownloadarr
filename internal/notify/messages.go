package notify

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/metadata/tmdb"
	"github.com/shyper/autodownloadarr/internal/store"
)

// MovieMessage captures artwork before a movie may be removed from the library.
func MovieMessage(kind string, m store.Movie, body string) events.Notification {
	if kind == events.NotifyMovieFailed {
		body = FailureReason(body)
	}
	title := m.Title
	if m.Year > 0 {
		title += fmt.Sprintf(" (%d)", m.Year)
	}
	status := map[string]string{events.NotifyMovieAvailable: "Movie ready", events.NotifyMovieFailed: "Movie failed", events.NotifyMovieDeleted: "Movie deleted"}[kind]
	return events.Notification{Event: kind, Title: status + ": " + title, Body: body, ImageURL: tmdb.PosterURL(m.PosterPath, "w500")}
}

// EpisodeMessage prefers episode artwork, then the season and series posters.
func EpisodeMessage(kind string, s store.Series, e store.Episode, body string) events.Notification {
	if kind == events.NotifyEpisodeFailed {
		body = FailureReason(body)
	}
	path := e.StillPath
	if path == "" {
		path = e.SeasonPosterPath
	}
	if path == "" {
		path = s.PosterPath
	}
	status := "Episode ready"
	if kind == events.NotifyEpisodeFailed {
		status = "Episode failed"
	}
	return events.Notification{Event: kind, Title: fmt.Sprintf("%s: %s S%02dE%02d", status, s.Title, e.Season, e.Episode), Subtitle: e.Title, Body: body, ImageURL: tmdb.PosterURL(path, "w500")}
}

// SeriesMessage captures a series poster before deletion.
func SeriesMessage(s store.Series) events.Notification {
	return events.Notification{Event: events.NotifySeriesDeleted, Title: "Series deleted: " + s.Title, ImageURL: tmdb.PosterURL(s.PosterPath, "w500")}
}

func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:max(0, limit-1)]) + "…"
}

func messageBody(msg events.Notification) string {
	parts := []string{}
	if msg.Subtitle != "" {
		parts = append(parts, msg.Subtitle)
	}
	if msg.Body != "" {
		parts = append(parts, msg.Body)
	}
	return strings.Join(parts, "\n\n")
}

func messageIcon(msg events.Notification) string {
	if icon := icons[msg.Event]; icon != "" {
		return icon
	}
	return "🔔"
}

// FailureReason replaces internal error codes with a readable summary, keeping details.
func FailureReason(raw string) string {
	code, detail, _ := strings.Cut(raw, ":")
	labels := map[string]string{
		"no_release": "No matching release found", "no_indexer": "No indexer configured",
		"indexer_error": "Indexer request failed", "no_usenet": "No Usenet server configured",
		"usenet_auth": "Usenet credentials were rejected", "no_movies_path": "Movie folder is not configured",
		"no_series_path": "Series folder is not configured", "sources_exhausted": "All download sources have been exhausted",
		"download_error": "Download failed", "disk_error": "Could not write the downloaded files",
		"incomplete": "Download is incomplete", "password": "Archive requires a password",
		"unpack": "Archive extraction failed", "no_video": "No video found in the download",
		"invalid_nzb": "Invalid NZB file", "spam": "Download was identified as spam", "cancelled": "Download cancelled",
	}
	label := labels[code]
	if label == "" {
		return raw
	}
	if strings.TrimSpace(detail) != "" {
		label += "\n" + strings.TrimSpace(detail)
	}
	return label
}

// clipBytes preserves UTF-8 while respecting ntfy's byte limit.
func clipBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	end := limit - len("…")
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}
