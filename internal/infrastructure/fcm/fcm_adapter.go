package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"io"
	"net/http"
	"strconv"
	"time"
)

type FCMAdapter struct {
	Client   *http.Client
	Endpoint string
	Tokens   oauth2.TokenSource
}

var _ Sender = (*FCMAdapter)(nil)

func NewFCMAdapter(ctx context.Context, project string) (*FCMAdapter, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 15 * time.Second})
	credentials, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/firebase.messaging")
	if err != nil {
		return nil, fmt.Errorf("load FCM credentials: %w", err)
	}
	if project == "" {
		project = credentials.ProjectID
	}
	if project == "" {
		return nil, fmt.Errorf("FCM project ID is required")
	}
	return &FCMAdapter{Client: &http.Client{Timeout: 15 * time.Second}, Endpoint: "https://fcm.googleapis.com/v1/projects/" + project + "/messages:send", Tokens: oauth2.ReuseTokenSource(nil, credentials.TokenSource)}, nil
}
func (f *FCMAdapter) Send(ctx context.Context, token string, data map[string]string) error {
	title, text := data["title"], data["body"]
	if title == "" {
		title = "AtoiTalk"
	}
	if text == "" {
		text = "Ada pesan baru"
	}
	body, err := json.Marshal(map[string]interface{}{"message": map[string]interface{}{"token": token, "notification": map[string]string{"title": title, "body": text}, "data": data, "webpush": map[string]interface{}{"notification": map[string]string{"icon": "/pwa-192x192.png", "tag": data["event_id"]}}, "android": map[string]interface{}{"ttl": "3600s"}, "apns": map[string]interface{}{"headers": map[string]string{"apns-expiration": strconv.FormatInt(time.Now().UTC().Add(time.Hour).Unix(), 10)}}}})
	if err != nil {
		return err
	}
	access, err := f.Tokens.Token()
	if err != nil {
		return fmt.Errorf("FCM credentials unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+access.AccessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := f.Client.Do(request)
	if err != nil {
		return &SendError{Retry: true}
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return &SendError{Retry: true}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	failure := &SendError{Status: response.StatusCode, Retry: response.StatusCode == 429 || response.StatusCode >= 500}
	var decoded struct {
		Error struct {
			Details []struct {
				Type string `json:"@type"`
				Code string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &decoded) == nil {
		for _, detail := range decoded.Error.Details {
			if detail.Type == "type.googleapis.com/google.firebase.fcm.v1.FcmError" && detail.Code == "UNREGISTERED" {
				failure.Invalid = true
			}
		}
	}
	if seconds, parseErr := strconv.Atoi(response.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
		failure.After = time.Duration(seconds) * time.Second
	} else if date, parseErr := http.ParseTime(response.Header.Get("Retry-After")); parseErr == nil {
		failure.After = time.Until(date)
	}
	return failure
}
