package movies

import (
	"context"
	"log/slog"

	"github.com/shyper/autodownloadarr/internal/mediaserver"
	"github.com/shyper/autodownloadarr/internal/store"
)

// ItemIndex is the library of one account, indexed by rating key and by a
// local path (a movie folder or an episode file).
type ItemIndex struct {
	ByKey  map[string]mediaserver.Item
	ByPath map[string]mediaserver.Item
}

// Lookup finds the item of a local path, falling back to the media key.
func (ix *ItemIndex) Lookup(path, mediaKey string) (mediaserver.Item, bool) {
	if it, ok := ix.ByPath[path]; ok {
		return it, true
	}
	it, ok := ix.ByKey[mediaKey]
	return it, ok
}

// AccountLibraries reads the library once per account during one poll. The
// watch state of a request comes from the media profile of its requester.
type AccountLibraries struct {
	svc    *Services
	target *Target
	log    *slog.Logger
	fetch  func(context.Context, mediaserver.Server) ([]mediaserver.Item, error)
	path   func(mediaserver.Item, string) string // item + remote file → local index path

	users   map[int64]string
	indexes map[string]*ItemIndex
}

// NewAccountLibraries prepares a poll. fetch lists the library of a client;
// path converts a remote file of an item into the local index path.
func NewAccountLibraries(svc *Services, target *Target, log *slog.Logger,
	fetch func(context.Context, mediaserver.Server) ([]mediaserver.Item, error),
	path func(mediaserver.Item, string) string,
) *AccountLibraries {
	return &AccountLibraries{svc: svc, target: target, log: log, fetch: fetch, path: path,
		users: map[int64]string{}, indexes: map[string]*ItemIndex{}}
}

// Account returns the account key for the requests of a user.
func (a *AccountLibraries) Account(ctx context.Context, userID int64) (string, error) {
	if key, ok := a.users[userID]; ok {
		return key, nil
	}
	key, err := a.svc.AccountForUser(ctx, userID)
	if err != nil {
		return "", err
	}
	a.users[userID] = key
	return key, nil
}

// Index returns the library as the account sees it. A failing profile is
// recorded on the profile and yields nil, so its requests are skipped this
// time; a failing default account is an error, as before profiles existed.
func (a *AccountLibraries) Index(ctx context.Context, key string) (*ItemIndex, error) {
	if ix, ok := a.indexes[key]; ok {
		return ix, nil
	}
	items, err := a.load(ctx, key)
	profileID := store.ProfileIDFromAccountKey(key)
	if err != nil {
		if key == "" {
			return nil, err
		}
		a.log.Warn("read watch state of media profile failed", "profile", profileID, "err", err)
		if serr := a.svc.Store.SetMediaProfileError(ctx, profileID, err.Error()); serr != nil {
			return nil, serr
		}
		a.indexes[key] = nil
		return nil, nil
	}
	if profileID > 0 {
		if err := a.svc.Store.SetMediaProfileError(ctx, profileID, ""); err != nil {
			return nil, err
		}
	}
	ix := &ItemIndex{ByKey: map[string]mediaserver.Item{}, ByPath: map[string]mediaserver.Item{}}
	for _, it := range items {
		ix.ByKey[it.Key] = it
		for _, f := range it.Files {
			ix.ByPath[a.path(it, f)] = it
		}
	}
	a.indexes[key] = ix
	return ix, nil
}

func (a *AccountLibraries) load(ctx context.Context, key string) ([]mediaserver.Item, error) {
	srv, err := a.target.ForAccount(ctx, key)
	if err != nil {
		return nil, err
	}
	return a.fetch(ctx, srv)
}

// SwitchAccount keeps a watch baseline tied to the account it was read
// from. When the account whose watch state counts changes (a profile was
// assigned or removed), a watched item stays watched and an unwatched one
// takes a new baseline from the new account.
func SwitchAccount(base *store.WatchBase, key string, watched bool, it mediaserver.Item) {
	if base.Account == key {
		return
	}
	switch {
	case !base.Set:
	case watched:
		// One completed viewing below the current count, as for watched movies.
		*base = store.WatchBase{Set: true, Views: it.ViewCount - 1, Offset: it.ViewOffset.Milliseconds()}
	default:
		*base = store.WatchBase{}
	}
	base.Account = key
}
