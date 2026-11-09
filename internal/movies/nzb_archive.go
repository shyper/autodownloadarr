package movies

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/shyper/autodownloadarr/internal/store"
)

// SaveNZB keeps the original document independently of download and media cleanup.
// Callers invoke it only when the global SaveNZBs setting is enabled.
func (s *Services) SaveNZB(rel store.Release, name string, data []byte) error {
	if s.NZBDir == "" {
		return errors.New("NZB archive directory is not configured")
	}
	if err := os.MkdirAll(s.NZBDir, 0750); err != nil {
		return fmt.Errorf("create NZB archive: %w", err)
	}
	if name == "" {
		name = rel.Title
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if strings.EqualFold(filepath.Ext(name), ".nzb") {
		name = name[:len(name)-4]
	}
	var safe strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			r = '_'
		}
		if safe.Len()+utf8.RuneLen(r) > 160 {
			break
		}
		safe.WriteRune(r)
	}
	name = strings.Trim(safe.String(), " .")
	if name == "" {
		name = "release"
	}
	digest := sha256.Sum256([]byte(rel.Identity()))
	target := filepath.Join(s.NZBDir, fmt.Sprintf("%x-%s.nzb", digest[:8], name))
	file, err := os.CreateTemp(s.NZBDir, ".nzb-*")
	if err != nil {
		return fmt.Errorf("create saved NZB: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write saved NZB: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("close saved NZB: %w", err)
	}
	if err = os.Rename(file.Name(), target); err != nil {
		return fmt.Errorf("save NZB: %w", err)
	}
	return nil
}
