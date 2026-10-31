package api

import "net/http"

func (s *server) handleTrending(w http.ResponseWriter, r *http.Request) {
	client, err := s.Services.TMDB(r.Context())
	if err != nil {
		s.writeStoreError(w, "tmdb", err)
		return
	}
	switch language := r.URL.Query().Get("language"); language {
	case "de", "en":
		client.Language = language
	}
	list, err := client.Trending(r.Context())
	if err != nil {
		s.writeStoreError(w, "trending", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
