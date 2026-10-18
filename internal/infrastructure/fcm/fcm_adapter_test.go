package fcm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFCMClassifiesProviderErrors(t *testing.T) {
	for _, tt := range []struct {
		name           string
		status         int
		body           string
		invalid, retry bool
	}{
		{"success", 200, `{"name":"projects/test/messages/1"}`, false, false},
		{"unregistered", 404, `{"error":{"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, true, false},
		{"bad credentials", 401, `{}`, false, false},
		{"rate limited", 429, `{}`, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer test", r.Header.Get("Authorization"))
				var payload struct {
					Message struct {
						Notification map[string]string `json:"notification"`
					} `json:"message"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				require.Equal(t, "Alya", payload.Message.Notification["title"])
				require.Equal(t, "Halo", payload.Message.Notification["body"])
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			sender := &FCMAdapter{Client: server.Client(), Endpoint: server.URL, Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"})}
			err := sender.Send(context.Background(), "device", map[string]string{"event_id": "1", "title": "Alya", "body": "Halo"})
			if tt.status == 200 {
				require.NoError(t, err)
			} else {
				failure, ok := err.(*SendError)
				require.True(t, ok)
				require.Equal(t, tt.invalid, failure.Invalid)
				require.Equal(t, tt.retry, failure.Retry)
			}
		})
	}
}

func TestNewFCMUsesApplicationDefaultCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"type":"authorized_user","client_id":"test","client_secret":"test","refresh_token":"test"}`), 0600))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	_, err := NewFCMAdapter(context.Background(), "")
	require.ErrorContains(t, err, "project ID")
	sender, err := NewFCMAdapter(context.Background(), "test-project")
	require.NoError(t, err)
	require.Equal(t, "https://fcm.googleapis.com/v1/projects/test-project/messages:send", sender.Endpoint)
	require.NoError(t, os.WriteFile(path, []byte(`invalid`), 0600))
	_, err = NewFCMAdapter(context.Background(), "test-project")
	require.ErrorContains(t, err, "credentials")
}

func TestFCMRetryAfterAndCanceledRequest(t *testing.T) {
	date := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Message struct {
				Notification map[string]string `json:"notification"`
			} `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if payload.Message.Notification["title"] != "AtoiTalk" || payload.Message.Notification["body"] != "Ada pesan baru" {
			t.Error("missing fallback preview")
		}
		w.Header().Set("Retry-After", date.Format(http.TimeFormat))
		w.WriteHeader(503)
	}))
	defer server.Close()
	sender := &FCMAdapter{Client: server.Client(), Endpoint: server.URL, Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"})}
	err := sender.Send(context.Background(), "device", nil)
	var failure *SendError
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Retry)
	require.InDelta(t, time.Until(date).Seconds(), failure.After.Seconds(), 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = sender.Send(ctx, "device", nil)
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Retry)
	sender.Endpoint = ":invalid"
	require.Error(t, sender.Send(context.Background(), "device", nil))
	sender.Tokens = failingTokenSource{}
	require.ErrorContains(t, sender.Send(context.Background(), "device", nil), "credentials unavailable")
}

type failingTokenSource struct{}

func (failingTokenSource) Token() (*oauth2.Token, error) {
	return nil, fmt.Errorf("provider unavailable")
}
