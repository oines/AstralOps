package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRelayClientListEnvelopesWithWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/relay/envelopes" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("device_id") != "dev_host" || r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("wait") != "10s" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer relay-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		writeJSON(w, http.StatusOK, map[string]any{"envelopes": []any{}})
	}))
	defer server.Close()

	client := RelayClient{BaseURL: server.URL, Token: "relay-token"}
	envelopes, err := client.ListRelayEnvelopesWait(context.Background(), "dev_host", 50, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelopes) != 0 {
		t.Fatalf("envelopes = %#v, want empty", envelopes)
	}
}
