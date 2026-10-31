package tmdb

import (
	"context"
	"strconv"
)

// TrendingTitle is a movie or TV show from the weekly discovery feed.
type TrendingTitle struct {
	ID        int    `json:"tmdbId"`
	MediaType string `json:"mediaType"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Overview  string `json:"overview"`
	PosterURL string `json:"posterUrl,omitempty"`
}

// Trending returns weekly movies and shows, excluding people and adult titles.
func (c *Client) Trending(ctx context.Context) ([]TrendingTitle, error) {
	var res struct {
		Results []struct {
			ID           int    `json:"id"`
			MediaType    string `json:"media_type"`
			Title        string `json:"title"`
			Name         string `json:"name"`
			ReleaseDate  string `json:"release_date"`
			FirstAirDate string `json:"first_air_date"`
			Overview     string `json:"overview"`
			PosterPath   string `json:"poster_path"`
			Adult        bool   `json:"adult"`
		} `json:"results"`
	}
	if err := c.get(ctx, "/trending/all/week", nil, &res); err != nil {
		return nil, err
	}
	out := make([]TrendingTitle, 0, len(res.Results))
	for _, item := range res.Results {
		if item.Adult || (item.MediaType != "movie" && item.MediaType != "tv") {
			continue
		}
		title, date := item.Title, item.ReleaseDate
		if item.MediaType == "tv" {
			title, date = item.Name, item.FirstAirDate
		}
		var year int
		if len(date) >= 4 {
			year, _ = strconv.Atoi(date[:4])
		}
		out = append(out, TrendingTitle{ID: item.ID, MediaType: item.MediaType, Title: title, Year: year,
			Overview: item.Overview, PosterURL: PosterURL(item.PosterPath, "w342")})
	}
	return out, nil
}
