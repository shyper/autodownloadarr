// Package mediaserver is the common interface to the media servers
// AutoDownloadarr supports (Plex, Jellyfin and Emby). The managers only use
// the neutral types and the Server interface, so a new server only needs an
// implementation of that interface.
package mediaserver

import (
	"context"
	"time"
)

// Kind identifies the media server software.
type Kind string

// Supported media servers.
const (
	Plex     Kind = "plex"
	Jellyfin Kind = "jellyfin"
	Emby     Kind = "emby"
)

// Valid reports whether k is a supported kind.
func (k Kind) Valid() bool {
	switch k {
	case Plex, Jellyfin, Emby:
		return true
	}
	return false
}

// Library is a library (Plex section or Jellyfin/Emby virtual folder).
type Library struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Type      string   `json:"type"` // movie, show, artist, photo
	Locations []string `json:"locations"`
}

// Item is a movie or episode in a library.
type Item struct {
	// Key is the server's unique ID of the item (Plex rating key).
	Key          string    `json:"ratingKey"`
	Title        string    `json:"title"`
	Year         int       `json:"year"`
	ViewCount    int       `json:"viewCount"`
	LastViewedAt time.Time `json:"lastViewedAt"`
	// ViewOffset is the resume position of a partly watched item.
	ViewOffset time.Duration `json:"viewOffset"`
	Duration   time.Duration `json:"duration"`
	Files      []string      `json:"files"`
	GUIDs      []string      `json:"guids"` // e.g. tmdb://693134, imdb://tt…
	// For episodes: the show and the season/episode numbers.
	ShowTitle string `json:"showTitle,omitempty"`
	Season    int    `json:"season,omitempty"`
	Episode   int    `json:"episode,omitempty"`
}

// Progress is the share of the item that has been watched (0..1).
func (i Item) Progress() float64 {
	if i.Watched() {
		return 1
	}
	if i.Duration <= 0 {
		return 0
	}
	return min(float64(i.ViewOffset)/float64(i.Duration), 1)
}

// Watched reports whether the account has watched the item completely.
func (i Item) Watched() bool { return i.ViewCount > 0 }

// Baseline is the watch state of an item at one moment. Media servers keep
// the history of a title when its file is deleted and added again, so only
// what changes after a new file appeared counts as watching it.
type Baseline struct {
	Views  int
	Offset time.Duration
}

// Baseline returns the current watch state.
func (i Item) Baseline() Baseline { return Baseline{Views: i.ViewCount, Offset: i.ViewOffset} }

// WatchedSince reports whether the item was watched completely after b.
func (i Item) WatchedSince(b Baseline) bool { return i.ViewCount > b.Views }

// ProgressSince is the share watched after b (0..1): 1 when watched since
// then, the resume position once it moved, else 0.
func (i Item) ProgressSince(b Baseline) float64 {
	if i.WatchedSince(b) {
		return 1
	}
	if i.ViewOffset == b.Offset || i.Duration <= 0 {
		return 0
	}
	return min(float64(i.ViewOffset)/float64(i.Duration), 1)
}

// Server is a configured media server.
type Server interface {
	// Identity returns a machine identifier and version; it doubles as a
	// connection test.
	Identity(ctx context.Context) (id, version string, err error)
	// Libraries lists the libraries with the paths the server sees.
	Libraries(ctx context.Context) ([]Library, error)
	// Scan asks the server to look at one folder of a library (a partial
	// scan where the server supports it). remotePath is the server's view.
	Scan(ctx context.Context, libraryKey, remotePath string) error
	// Movies lists the movies of a library.
	Movies(ctx context.Context, libraryKey string) ([]Item, error)
	// Episodes lists the episodes of a TV library.
	Episodes(ctx context.Context, libraryKey string) ([]Item, error)
}
