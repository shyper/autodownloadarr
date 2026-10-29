package api

import (
	"net/http"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/notify"
	"github.com/shyper/autodownloadarr/internal/store"
)

// --- notifications ----------------------------------------------------------

func (s *server) handleGetNotification(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.Notification(r.Context())
	if err != nil {
		s.internalError(w, "read notification settings", err)
		return
	}
	n.Token = ""
	writeJSON(w, http.StatusOK, n)
}

func (s *server) handlePutNotification(w http.ResponseWriter, r *http.Request) {
	var n store.Notification
	if !decodeJSON(w, r, &n) {
		return
	}
	if err := s.Store.SetNotification(r.Context(), n); err != nil {
		s.writeStoreError(w, "save notification settings", err)
		return
	}
	s.handleGetNotification(w, r)
}

// handleTestNotification sends a test message to the submitted channel. An
// empty token uses the stored one.
func (s *server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	var n store.Notification
	if !decodeJSON(w, r, &n) {
		return
	}
	if n.Token == "" {
		if cur, err := s.Store.Notification(r.Context()); err == nil {
			n.Token = cur.Token
		}
	}
	svc := notify.New(s.Store, s.Log)
	if s.Services != nil && s.Services.HTTP != nil {
		svc.HTTP = s.Services.HTTP
	}
	msg := events.Notification{Event: "test", Title: "AutoDownloadarr", Body: "This is a test notification."}
	if err := svc.SendTo(r.Context(), n, msg); err != nil {
		s.writeStoreError(w, "send test notification", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
