package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/shyper/autodownloadarr/internal/auth"
)

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.DB.PingContext(ctx); err != nil {
		s.Log.Error("health check: database unavailable", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	required, err := s.Auth.SetupRequired(r.Context())
	if err != nil {
		s.internalError(w, "check setup state", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":       s.Version,
		"setupRequired": required,
	})
}

type setupRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *server) handleSetup(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if ok, retry := s.Limiter.Allow(ip); !ok {
		writeTooManyAttempts(w, retry)
		return
	}
	var req setupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	required, err := s.Auth.SetupRequired(r.Context())
	if err != nil {
		s.internalError(w, "check setup state", err)
		return
	}
	if !required {
		writeError(w, http.StatusConflict, "setup_completed", auth.ErrSetupCompleted.Error())
		return
	}
	if !auth.SetupTokenMatches(s.SetupToken, req.Token) {
		s.Limiter.Fail(ip)
		s.Log.Warn("setup attempt with invalid token", "ip", ip)
		writeError(w, http.StatusForbidden, "invalid_setup_token", "invalid setup token")
		return
	}

	user, err := s.Auth.CreateInitialAdmin(r.Context(), req.Username, req.Password)
	switch {
	case errors.Is(err, auth.ErrSetupCompleted):
		writeError(w, http.StatusConflict, "setup_completed", err.Error())
		return
	case errors.Is(err, auth.ErrInvalidUsername):
		writeError(w, http.StatusBadRequest, "invalid_username", err.Error())
		return
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	case err != nil:
		s.internalError(w, "create admin", err)
		return
	}
	s.Log.Info("initial admin account created", "username", user.Username)
	s.startSession(w, r, user, http.StatusCreated)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if ok, retry := s.Limiter.Allow(ip); !ok {
		writeTooManyAttempts(w, retry)
		return
	}
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, err := s.Auth.Authenticate(r.Context(), req.Username, req.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.Limiter.Fail(ip)
		// Logged so that tools like fail2ban can act on it.
		s.Log.Warn("failed login", "username", req.Username, "ip", ip)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
		return
	}
	if err != nil {
		s.internalError(w, "authenticate", err)
		return
	}
	s.Limiter.Reset(ip)
	s.startSession(w, r, user, http.StatusOK)
}

func (s *server) startSession(w http.ResponseWriter, r *http.Request, user auth.User, status int) {
	token, sess, err := s.Auth.CreateSession(r.Context(), user.ID, r.UserAgent(), s.clientIP(r))
	if err != nil {
		s.internalError(w, "create session", err)
		return
	}
	s.setSessionCookie(w, r, token, sess.ExpiresAt)
	writeJSON(w, status, map[string]any{"user": user})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Auth.DeleteSession(r.Context(), c.Value); err != nil {
			s.internalError(w, "delete session", err)
			return
		}
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _, _ := currentUser(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	user, sess, _ := currentUser(r.Context())
	err := s.Auth.ChangePassword(r.Context(), user.ID, sess.ID, req.CurrentPassword, req.NewPassword)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusForbidden, "invalid_current_password", "current password is wrong")
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
	case err != nil:
		s.internalError(w, "change password", err)
	default:
		s.Log.Info("password changed", "username", user.Username)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleEvents streams bus events to the web UI as Server-Sent Events.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // the stream outlives the server's write timeout

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // disable buffering in nginx
	w.WriteHeader(http.StatusOK)

	ch, unsubscribe := s.Bus.Subscribe(64)
	defer unsubscribe()

	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil || rc.Flush() != nil {
		return
	}
	ticker := time.NewTicker(s.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.Done:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				s.Log.Error("encode event", "type", ev.Type, "err", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
				return
			}
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		}
		if rc.Flush() != nil {
			return
		}
	}
}

func (s *server) internalError(w http.ResponseWriter, what string, err error) {
	s.Log.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}
