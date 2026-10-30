package api

import (
	"errors"
	"github.com/shyper/autodownloadarr/internal/access"
	"net/http"
)

func (s *server) requireOwner(kind string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _, _ := currentUser(r.Context())
			id, _ := idParam(r, "id")
			err := access.Check(r.Context(), s.Store, user, kind, id)
			if errors.Is(err, access.ErrForbidden) {
				writeError(w, http.StatusForbidden, "forbidden", "only the request owner or an administrator may change this item")
				return
			}
			if err != nil {
				s.writeStoreError(w, "check ownership", err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
