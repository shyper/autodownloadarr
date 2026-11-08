package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/httpx"
	"github.com/shyper/autodownloadarr/internal/store"
)

const defaultPushoverURL = "https://api.pushover.net/1/messages.json"
const maxAttachmentBytes = 2621440

func (s *Service) sendPushover(ctx context.Context, n store.Notification, msg events.Notification) error {
	if n.Token == "" || n.UserKey == "" {
		return fmt.Errorf("%w: Pushover application token and user/group key are required", store.ErrInvalid)
	}
	body := messageBody(msg)
	if body == "" {
		body = msg.Title
	}
	values := url.Values{"token": {n.Token}, "user": {n.UserKey}, "title": {clip(messageIcon(msg)+" "+msg.Title, 250)}, "message": {clip(body, 1024)}}
	if n.Device != "" {
		values.Set("device", n.Device)
	}
	// Only artwork created from our TMDB metadata is downloaded. No user URLs.
	if data, contentType := s.artwork(ctx, msg.ImageURL); len(data) > 0 {
		values.Set("attachment_base64", base64.StdEncoding.EncodeToString(data))
		values.Set("attachment_type", contentType)
	}
	target := s.PushoverURL
	if target == "" {
		target = defaultPushoverURL
	}
	return s.postPushover(ctx, target, values)
}

func (s *Service) artwork(ctx context.Context, rawURL string) ([]byte, string) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host != "image.tmdb.org" || u.User != nil {
		return nil, ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ""
	}
	client := s.HTTP
	if client == nil {
		client = httpx.NewClient()
	}
	// Keep image failures from consuming the notification's entire timeout.
	imageCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, _, err := httpx.Do(client, req.WithContext(imageCtx), maxAttachmentBytes+1)
	if err != nil || len(data) > maxAttachmentBytes {
		return nil, ""
	}
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/jpeg", "image/png", "image/gif":
		return data, contentType
	}
	return nil, ""
}

func (s *Service) postPushover(ctx context.Context, target string, values url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := s.HTTP
	if client == nil {
		client = httpx.NewClient()
	}
	data, _, err := httpx.Do(client, req, 1<<20)
	var response struct {
		Status int `json:"status"`
	}
	if err == nil && json.Unmarshal(data, &response) == nil && response.Status == 1 {
		return nil
	}
	// Pushover may reject an otherwise valid notification because of its image.
	// Retry a validation failure once without the image, never rate limits or network errors.
	var status *httpx.StatusError
	if values.Has("attachment_base64") && errors.As(err, &status) && status.Code == http.StatusBadRequest {
		values.Del("attachment_base64")
		values.Del("attachment_type")
		return s.postPushover(ctx, target, values)
	}
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return fmt.Errorf("notify: Pushover did not accept the notification")
}
