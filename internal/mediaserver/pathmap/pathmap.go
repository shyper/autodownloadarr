// Package pathmap translates paths between this container and a media
// server that sees the same files under different paths, e.g. /movies in
// the container and /mnt/hdd/movies (or D:\Movies) on the Plex host.
package pathmap

import (
	"path"
	"slices"
	"strings"
)

// Mapping pairs a local path with the media server's path for it.
type Mapping struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
}

// Mapper converts paths in both directions. The longest matching prefix
// wins, and prefixes only match at directory boundaries.
type Mapper struct {
	mappings []Mapping
}

// New returns a mapper. Without mappings paths are passed through.
func New(mappings []Mapping) Mapper {
	m := Mapper{}
	for _, mp := range mappings {
		if mp.Local = cleanLocal(mp.Local); mp.Local == "" || strings.TrimSpace(mp.Remote) == "" {
			continue
		}
		mp.Remote = strings.TrimRight(strings.TrimSpace(mp.Remote), `/\`)
		m.mappings = append(m.mappings, mp)
	}
	return m
}

// ToRemote converts a local path to the media server's view.
func (m Mapper) ToRemote(local string) string {
	local = cleanLocal(local)
	best, rest := -1, ""
	for i, mp := range m.mappings {
		if r, ok := cutPrefix(local, mp.Local, false); ok && (best < 0 || len(mp.Local) > len(m.mappings[best].Local)) {
			best, rest = i, r
		}
	}
	if best < 0 {
		return local
	}
	remote := m.mappings[best].Remote
	if windows(remote) {
		return remote + strings.ReplaceAll(rest, "/", `\`)
	}
	return remote + rest
}

// ToLocal converts a path reported by the media server to a local path.
func (m Mapper) ToLocal(remote string) string {
	best, rest := -1, ""
	for i, mp := range m.mappings {
		r, ok := cutPrefix(remote, mp.Remote, windows(mp.Remote))
		if ok && (best < 0 || len(mp.Remote) > len(m.mappings[best].Remote)) {
			best, rest = i, r
		}
	}
	if best < 0 {
		return cleanLocal(remote)
	}
	return cleanLocal(m.mappings[best].Local + strings.ReplaceAll(rest, `\`, "/"))
}

// Suggest proposes mappings by matching local folders with media server
// library folders that end in the same directory name
// (/movies ↔ /mnt/hdd/movies).
func Suggest(localRoots, remoteLocations []string) []Mapping {
	var out []Mapping
	for _, local := range localRoots {
		local = cleanLocal(local)
		name := strings.ToLower(path.Base(local))
		for _, remote := range remoteLocations {
			r := strings.TrimRight(remote, `/\`)
			if strings.ToLower(baseName(r)) == name && !slices.Contains(out, Mapping{Local: local, Remote: r}) {
				out = append(out, Mapping{Local: local, Remote: r})
			}
		}
	}
	return out
}

// Contains reports whether remote lies inside one of the library folders.
func Contains(libraryFolders []string, remote string) bool {
	for _, f := range libraryFolders {
		f = strings.TrimRight(f, `/\`)
		if _, ok := cutPrefix(remote, f, windows(f)); ok {
			return true
		}
	}
	return false
}

func cleanLocal(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

// cutPrefix removes prefix from p if it is followed by a separator or the
// end of p. The remainder starts with a separator or is empty.
func cutPrefix(p, prefix string, foldCase bool) (string, bool) {
	if len(p) < len(prefix) {
		return "", false
	}
	head := p[:len(prefix)]
	if head != prefix && (!foldCase || !strings.EqualFold(head, prefix)) {
		return "", false
	}
	rest := p[len(prefix):]
	if rest != "" && rest[0] != '/' && rest[0] != '\\' && prefix != "/" {
		return "", false
	}
	if prefix == "/" && rest != "" && rest[0] != '/' {
		rest = "/" + rest
	}
	return rest, true
}

// windows reports whether p looks like a Windows path (C:\… or \\server).
func windows(p string) bool {
	return strings.Contains(p, `\`) || (len(p) >= 2 && p[1] == ':')
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
