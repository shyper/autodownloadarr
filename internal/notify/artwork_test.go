package notify

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/store"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestArtworkPreference(t *testing.T) {
	for _, tc := range []struct{ still, season, series, want string }{
		{"/episode.jpg", "/season.jpg", "/series.jpg", "/episode.jpg"},
		{"", "/season.jpg", "/series.jpg", "/season.jpg"},
		{"", "", "/series.jpg", "/series.jpg"},
		{"", "", "", ""},
	} {
		se := store.Series{Title: "Show", PosterPath: tc.series}
		ep := store.Episode{Season: 2, Episode: 5, Title: "Episode title", StillPath: tc.still, SeasonPosterPath: tc.season}
		msg := EpisodeMessage(events.NotifyEpisodeAvailable, se, ep, "release")
		if tc.want == "" && msg.ImageURL != "" || tc.want != "" && !strings.HasSuffix(msg.ImageURL, tc.want) {
			t.Errorf("artwork = %s, want %s", msg.ImageURL, tc.want)
		}
		if msg.Subtitle != "Episode title" || !strings.Contains(msg.Title, "Show S02E05") || msg.Body != "release" {
			t.Errorf("message = %+v", msg)
		}
	}
	msg := MovieMessage(events.NotifyMovieAvailable, store.Movie{Title: "Film", Year: 2026, PosterPath: "/film.jpg"}, "release")
	if msg.Title != "Movie ready: Film (2026)" || !strings.HasSuffix(msg.ImageURL, "/film.jpg") {
		t.Errorf("movie = %+v", msg)
	}
}

func TestTelegramPhotoAndTextFallback(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if strings.HasSuffix(r.URL.Path, "sendPhoto") {
					if body["photo"] != "https://image.tmdb.org/t/p/w500/ep.jpg" || body["parse_mode"] != "HTML" || !strings.Contains(fmt.Sprint(body["caption"]), "Episode &amp; title") {
						t.Errorf("photo = %+v", body)
					}
					if reject {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
				} else if !strings.Contains(fmt.Sprint(body["text"]), "Episode &amp; title") {
					t.Errorf("text = %+v", body)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()
			svc := &Service{HTTP: srv.Client(), TelegramURL: srv.URL}
			err := svc.SendTo(t.Context(), store.Notification{Provider: "telegram", Token: "secret", ChatID: "42"}, events.Notification{Title: "Show", Subtitle: "Episode & title", Body: "release", ImageURL: "https://image.tmdb.org/t/p/w500/ep.jpg"})
			if err != nil {
				t.Fatal(err)
			}
			if len(paths) != 1 && !reject || reject && (len(paths) != 2 || paths[1] != "/botsecret/sendMessage") {
				t.Fatalf("paths = %v", paths)
			}
		})
	}
}

func TestTelegramCaptionLimit(t *testing.T) {
	msg := events.Notification{Event: events.NotifyEpisodeAvailable, Title: strings.Repeat("界", 500), Subtitle: strings.Repeat("é", 500), Body: strings.Repeat("<&", 2000)}
	caption := telegramCaption(msg, 1024)
	plain := strings.NewReplacer("<b>", "", "</b>", "", "<code>", "", "</code>", "").Replace(caption)
	if len([]rune(html.UnescapeString(plain))) > 1024 {
		t.Fatal("caption exceeded the Telegram limit")
	}
	if !strings.HasSuffix(caption, "</code>") {
		t.Fatal("caption has broken HTML")
	}
}

func TestSendPushover(t *testing.T) {
	for _, tc := range []struct {
		name, image                    string
		failDownload, rejectAttachment bool
		response                       string
		wantError                      bool
	}{
		{name: "text", response: `{"status":1}`},
		{name: "image", image: "https://image.tmdb.org/t/p/w500/image.png", response: `{"status":1}`},
		{name: "missing image", image: "https://image.tmdb.org/t/p/w500/image.png", failDownload: true, response: `{"status":1}`},
		{name: "rejected attachment", image: "https://image.tmdb.org/t/p/w500/image.png", rejectAttachment: true, response: `{"status":1}`},
		{name: "provider failure", response: `{"status":0}`, wantError: true},
		{name: "invalid response", response: `not json`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			svc := &Service{HTTP: &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "image.tmdb.org" {
					if tc.failDownload {
						return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("missing")), Request: r}, nil
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("\x89PNG\r\n\x1a\n")), Request: r}, nil
				}
				requests++
				if r.URL.String() != defaultPushoverURL || r.Method != "POST" {
					t.Errorf("target = %s %s", r.Method, r.URL)
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Get("token") != "app-token" || r.Form.Get("user") != "user-key" || r.Form.Get("device") != "phone" || r.Form.Get("message") != "Episode\n\nrelease" {
					t.Errorf("unexpected form fields")
				}
				hasImage := r.Form.Get("attachment_base64") != ""
				wantImage := tc.image != "" && !tc.failDownload && requests == 1
				if hasImage != wantImage {
					t.Errorf("attachment present = %v, want %v", hasImage, wantImage)
				}
				status := 200
				if hasImage && tc.rejectAttachment {
					status = 400
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.response)), Request: r}, nil
			})}}
			err := svc.SendTo(t.Context(), store.Notification{Provider: "pushover", Token: "app-token", UserKey: "user-key", Device: "phone"}, events.Notification{Title: "Show", Subtitle: "Episode", Body: "release", ImageURL: tc.image})
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v", err)
			}
			wantRequests := 1
			if tc.rejectAttachment {
				wantRequests = 2
			}
			if requests != wantRequests {
				t.Errorf("sent %d requests, want %d", requests, wantRequests)
			}
		})
	}
}

func TestImagePayloads(t *testing.T) {
	for _, provider := range []string{"webhook", "discord", "ntfy"} {
		t.Run(provider, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if provider == "ntfy" {
					if r.Header.Get("Attach") != "https://image.tmdb.org/t/p/w500/ep.jpg" {
						t.Error("missing ntfy attachment")
					}
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if provider == "webhook" {
					if body["imageUrl"] != "https://image.tmdb.org/t/p/w500/ep.jpg" || body["subtitle"] != "Episode" || body["message"] != "release" {
						t.Errorf("webhook = %+v", body)
					}
				} else {
					embed := body["embeds"].([]any)[0].(map[string]any)
					if embed["image"].(map[string]any)["url"] != "https://image.tmdb.org/t/p/w500/ep.jpg" {
						t.Error("missing Discord image")
					}
					if len(body["allowed_mentions"].(map[string]any)["parse"].([]any)) != 0 {
						t.Error("mentions not disabled")
					}
				}
			}))
			defer srv.Close()
			svc := &Service{HTTP: srv.Client()}
			if err := svc.SendTo(t.Context(), store.Notification{Provider: provider, URL: srv.URL}, events.Notification{Title: "Show", Subtitle: "Episode", Body: "release", ImageURL: "https://image.tmdb.org/t/p/w500/ep.jpg"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNtfyUTF8ByteLimit(t *testing.T) {
	text := strings.Repeat("🎬界é", 2000)
	got := clipBytes(text, 4096)
	if len(got) > 4096 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatal("ntfy text exceeds the byte limit or has invalid UTF-8")
	}
}

func TestFailureReason(t *testing.T) {
	got := MovieMessage(events.NotifyMovieFailed, store.Movie{Title: "Film"}, "disk_error: permission denied")
	if got.Body != "Could not write the downloaded files\npermission denied" {
		t.Errorf("reason = %s", got.Body)
	}
	if FailureReason("unknown_error: diagnostic") != "unknown_error: diagnostic" {
		t.Fatal("unknown failure details lost")
	}
}
