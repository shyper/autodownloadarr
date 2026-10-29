package api

import (
	"errors"
	"net/http"

	"github.com/shyper/autodownloadarr/internal/auth"
)

type createUserRequest struct {
	Username string    `json:"username"`
	Password string    `json:"password"`
	Role     auth.Role `json:"role"`
}

type updateUserRequest struct {
	Role auth.Role `json:"role"`
}

type resetUserPasswordRequest struct {
	NewPassword string `json:"newPassword"`
}

func (s *server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Auth.Users(r.Context())
	if err != nil {
		s.internalError(w, "list users", err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := s.Auth.CreateUser(r.Context(), req.Username, req.Password, req.Role)
	if err != nil {
		s.writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		s.writeUserError(w, auth.ErrUserNotFound)
		return
	}
	var req updateUserRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := s.Auth.UpdateRole(r.Context(), id, req.Role)
	if err != nil {
		s.writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		s.writeUserError(w, auth.ErrUserNotFound)
		return
	}
	if err := s.Auth.DeleteUser(r.Context(), id); err != nil {
		s.writeUserError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		s.writeUserError(w, auth.ErrUserNotFound)
		return
	}
	var req resetUserPasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.Auth.SetUserPassword(r.Context(), id, req.NewPassword); err != nil {
		s.writeUserError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) writeUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, auth.ErrUserExists):
		writeError(w, http.StatusConflict, "username_taken", err.Error())
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", err.Error())
	case errors.Is(err, auth.ErrInvalidUsername), errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrInvalidRole):
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
	default:
		s.internalError(w, "user management", err)
	}
}
