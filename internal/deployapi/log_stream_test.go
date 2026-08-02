package deployapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Decoding must key off logStreamType, not the position of a variant in the
// spec's oneOf. The generated LogStreamConfig0..5 accessors ignore the
// discriminator, so a payload would otherwise decode into whichever variant
// happened to be listed first.
func TestDecodeLogStreamUsesDiscriminator(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantType LogStreamType
		check    func(*testing.T, *LogStream)
	}{
		{
			name: "axiom",
			payload: `{"logStreamType":"axiom","id":"ls_1","status":{"type":"active"},
			           "datasetName":"convex","attributes":[{"key":"env","value":"prod"}]}`,
			wantType: LogStreamAxiom,
			check: func(t *testing.T, s *LogStream) {
				if s.Axiom == nil {
					t.Fatal("Axiom sink is nil")
				}

				if s.Axiom.DatasetName != "convex" {
					t.Errorf("dataset = %q, want convex", s.Axiom.DatasetName)
				}

				if len(s.Axiom.Attributes) != 1 || s.Axiom.Attributes[0].Key != "env" {
					t.Errorf("attributes = %v", s.Axiom.Attributes)
				}
			},
		},
		{
			name: "datadog",
			payload: `{"logStreamType":"datadog","id":"ls_2","status":{"type":"pending"},
			           "siteLocation":"EU","ddTags":["team:core"]}`,
			wantType: LogStreamDatadog,
			check: func(t *testing.T, s *LogStream) {
				if s.Datadog == nil {
					t.Fatal("Datadog sink is nil")
				}

				if string(s.Datadog.SiteLocation) != "EU" {
					t.Errorf("siteLocation = %q, want EU", s.Datadog.SiteLocation)
				}
			},
		},
		{
			name: "webhook",
			payload: `{"logStreamType":"webhook","id":"ls_3","status":{"type":"active"},
			           "url":"https://example.com/hook","format":"json","hmacSecret":"shh"}`,
			wantType: LogStreamWebhook,
			check: func(t *testing.T, s *LogStream) {
				if s.Webhook == nil {
					t.Fatal("Webhook sink is nil")
				}

				if s.Webhook.HmacSecret != "shh" {
					t.Errorf("hmacSecret = %q, want shh", s.Webhook.HmacSecret)
				}
			},
		},
		{
			name:     "sentry",
			payload:  `{"logStreamType":"sentry","id":"ls_4","status":{"type":"active"},"tags":{"env":"prod"}}`,
			wantType: LogStreamSentry,
			check: func(t *testing.T, s *LogStream) {
				if s.Sentry == nil {
					t.Fatal("Sentry sink is nil")
				}
			},
		},
		{
			name:     "posthog logs",
			payload:  `{"logStreamType":"posthog_logs","id":"ls_5","status":{"type":"active"},"host":"https://eu.posthog.com"}`,
			wantType: LogStreamPostHogLogs,
			check: func(t *testing.T, s *LogStream) {
				if s.PostHogLogs == nil {
					t.Fatal("PostHogLogs sink is nil")
				}
			},
		},
		{
			name:     "posthog error tracking",
			payload:  `{"logStreamType":"posthog_error_tracking","id":"ls_6","status":{"type":"active"}}`,
			wantType: LogStreamPostHogErrorTracking,
			check: func(t *testing.T, s *LogStream) {
				if s.PostHogErrorTracking == nil {
					t.Fatal("PostHogErrorTracking sink is nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream, err := DecodeLogStream([]byte(tt.payload))
			if err != nil {
				t.Fatalf("DecodeLogStream() error = %v", err)
			}

			if stream.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", stream.Type, tt.wantType)
			}

			if stream.ID == "" {
				t.Error("ID is empty")
			}

			// Exactly one sink must be populated.
			populated := 0

			for _, isSet := range []bool{
				stream.Datadog != nil, stream.Webhook != nil, stream.Axiom != nil,
				stream.Sentry != nil, stream.PostHogLogs != nil, stream.PostHogErrorTracking != nil,
			} {
				if isSet {
					populated++
				}
			}

			if populated != 1 {
				t.Errorf("%d sinks populated, want exactly 1", populated)
			}

			tt.check(t, stream)
		})
	}
}

func TestDecodeLogStreamStatus(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		wantType   string
		wantReason string
	}{
		{name: "active", status: `{"type":"active"}`, wantType: "active"},
		{name: "pending", status: `{"type":"pending"}`, wantType: "pending"},
		{
			name:     "failed carries a reason",
			status:   `{"type":"failed","reason":"invalid api key"}`,
			wantType: "failed", wantReason: "invalid api key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := `{"logStreamType":"sentry","id":"ls_1","status":` + tt.status + `}`

			stream, err := DecodeLogStream([]byte(payload))
			if err != nil {
				t.Fatalf("DecodeLogStream() error = %v", err)
			}

			if stream.Status.Type != tt.wantType {
				t.Errorf("status type = %q, want %q", stream.Status.Type, tt.wantType)
			}

			if stream.Status.Reason != tt.wantReason {
				t.Errorf("status reason = %q, want %q", stream.Status.Reason, tt.wantReason)
			}
		})
	}
}

func TestDecodeLogStreamRejectsUnknown(t *testing.T) {
	if _, err := DecodeLogStream([]byte(`{"id":"ls_1"}`)); err == nil {
		t.Error("payload with no logStreamType decoded without error")
	}

	if _, err := DecodeLogStream([]byte(`{"logStreamType":"splunk","id":"ls_1"}`)); err == nil {
		t.Error("payload with an unknown logStreamType decoded without error")
	}
}

// The webhook secret is returned once, at creation.
func TestCreateLogStreamReturnsWebhookSecret(t *testing.T) {
	var sent map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logStreamType":"webhook","id":"ls_9","hmacSecret":"generated"}`))
	}))
	defer server.Close()

	client, err := NewAuthenticated(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}

	created, err := CreateLogStream(t.Context(), client, map[string]any{
		"logStreamType": "webhook",
		"url":           "https://example.com/hook",
		"format":        "json",
	})
	if err != nil {
		t.Fatalf("CreateLogStream() error = %v", err)
	}

	if created.ID != "ls_9" {
		t.Errorf("ID = %q, want ls_9", created.ID)
	}

	if created.HMACSecret != "generated" {
		t.Errorf("HMACSecret = %q, want generated", created.HMACSecret)
	}

	if sent["logStreamType"] != "webhook" {
		t.Errorf("sent logStreamType = %v, want webhook", sent["logStreamType"])
	}
}

// Non-webhook sinks return an id and no secret.
func TestCreateLogStreamWithoutSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logStreamType":"axiom","id":"ls_10"}`))
	}))
	defer server.Close()

	client, err := NewAuthenticated(server.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}

	created, err := CreateLogStream(t.Context(), client, map[string]any{"logStreamType": "axiom"})
	if err != nil {
		t.Fatalf("CreateLogStream() error = %v", err)
	}

	if created.HMACSecret != "" {
		t.Errorf("HMACSecret = %q, want empty", created.HMACSecret)
	}
}
