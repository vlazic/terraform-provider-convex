package deployapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/srs/terraform-provider-convex/internal/apierr"
)

// LogStreamType discriminates the sink a log stream writes to. The API carries
// it as the `logStreamType` field on every log stream payload.
type LogStreamType string

const (
	LogStreamDatadog              LogStreamType = "datadog"
	LogStreamWebhook              LogStreamType = "webhook"
	LogStreamAxiom                LogStreamType = "axiom"
	LogStreamSentry               LogStreamType = "sentry"
	LogStreamPostHogLogs          LogStreamType = "posthog_logs"
	LogStreamPostHogErrorTracking LogStreamType = "posthog_error_tracking"
)

// LogStreamTypes lists every sink, in the order the schema declares them.
var LogStreamTypes = []LogStreamType{
	LogStreamDatadog,
	LogStreamWebhook,
	LogStreamAxiom,
	LogStreamSentry,
	LogStreamPostHogLogs,
	LogStreamPostHogErrorTracking,
}

// ErrLogStreamNotFound reports that no log stream with that id exists.
var ErrLogStreamNotFound = errors.New("log stream not found")

// LogStream is a decoded LogStreamConfig: the shared fields, plus exactly one
// populated sink.
//
// The generated union exposes its variants positionally
// (LogStreamConfig0 ... LogStreamConfig5) with no regard for the discriminator,
// so decoding through it would both mis-assign a payload whose `oneOf` order
// changed upstream and silently accept the wrong variant. Everything here
// keys off `logStreamType` and the stable per-sink types instead.
type LogStream struct {
	ID     string
	Type   LogStreamType
	Status LogStreamState

	Datadog              *DatadogLogStreamConfig
	Webhook              *WebhookLogStreamConfig
	Axiom                *AxiomLogStreamConfig
	Sentry               *SentryLogStreamConfig
	PostHogLogs          *PostHogLogsLogStreamConfig
	PostHogErrorTracking *PostHogErrorTrackingLogStreamConfig
}

// LogStreamState is a decoded LogStreamStatus. Reason is set only when the
// stream has failed.
type LogStreamState struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

// CreatedLogStream is what the create endpoint reports back. HMACSecret is set
// only for webhook sinks, and is the one time Convex reveals it.
type CreatedLogStream struct {
	ID         string
	Type       LogStreamType
	HMACSecret string
}

// discriminator pulls `logStreamType` out of an arbitrary payload.
func discriminator(raw []byte) (LogStreamType, error) {
	var probe struct {
		LogStreamType string `json:"logStreamType"`
	}

	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", fmt.Errorf("reading logStreamType: %w", err)
	}

	if probe.LogStreamType == "" {
		return "", errors.New("log stream payload has no logStreamType")
	}

	return LogStreamType(probe.LogStreamType), nil
}

// DecodeLogStream converts a raw log stream payload into a LogStream.
func DecodeLogStream(raw []byte) (*LogStream, error) {
	streamType, err := discriminator(raw)
	if err != nil {
		return nil, err
	}

	stream := &LogStream{Type: streamType}

	// Decode into the named struct for this sink, then lift the shared fields.
	// Each per-sink config carries its own id and status.
	var status LogStreamStatus

	switch streamType {
	case LogStreamDatadog:
		var c DatadogLogStreamConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("decoding datadog log stream: %w", err)
		}

		stream.Datadog, stream.ID, status = &c, c.Id, c.Status

	case LogStreamWebhook:
		var c WebhookLogStreamConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("decoding webhook log stream: %w", err)
		}

		stream.Webhook, stream.ID, status = &c, c.Id, c.Status

	case LogStreamAxiom:
		var c AxiomLogStreamConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("decoding axiom log stream: %w", err)
		}

		stream.Axiom, stream.ID, status = &c, c.Id, c.Status

	case LogStreamSentry:
		var c SentryLogStreamConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("decoding sentry log stream: %w", err)
		}

		stream.Sentry, stream.ID, status = &c, c.Id, c.Status

	case LogStreamPostHogLogs:
		var c PostHogLogsLogStreamConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("decoding posthog logs log stream: %w", err)
		}

		stream.PostHogLogs, stream.ID, status = &c, c.Id, c.Status

	case LogStreamPostHogErrorTracking:
		var c PostHogErrorTrackingLogStreamConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("decoding posthog error tracking log stream: %w", err)
		}

		stream.PostHogErrorTracking, stream.ID, status = &c, c.Id, c.Status

	default:
		return nil, fmt.Errorf("unknown log stream type %q", streamType)
	}

	state, err := decodeStatus(status)
	if err != nil {
		return nil, err
	}

	stream.Status = state

	return stream, nil
}

// decodeStatus flattens the status union. Every variant carries `type`; only
// the failed one carries `reason`, so both decode from the same shape.
func decodeStatus(status LogStreamStatus) (LogStreamState, error) {
	raw, err := status.MarshalJSON()
	if err != nil {
		return LogStreamState{}, fmt.Errorf("reading log stream status: %w", err)
	}

	var state LogStreamState

	if err := json.Unmarshal(raw, &state); err != nil {
		return LogStreamState{}, fmt.Errorf("decoding log stream status: %w", err)
	}

	return state, nil
}

// CreateLogStream creates a log stream from a payload that already carries its
// `logStreamType`.
//
// Deliberately not retried: a failed create does not prove nothing was made,
// so retrying could leave a duplicate stream behind.
//
// The body is passed pre-encoded rather than through the generated union type,
// whose variants are positional and would silently rebind if the spec's
// `oneOf` order changed.
func CreateLogStream(ctx context.Context, client *ClientWithResponses, payload any) (*CreatedLogStream, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding log stream: %w", err)
	}

	created, err := client.CreateLogStreamWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	if err := CheckResponse(created); err != nil {
		return nil, err
	}

	// Every variant of the response carries an id; webhook adds hmacSecret.
	var decoded struct {
		Id            string `json:"id"`
		LogStreamType string `json:"logStreamType"`
		HmacSecret    string `json:"hmacSecret"`
	}

	if err := json.Unmarshal(created.Body, &decoded); err != nil {
		return nil, fmt.Errorf("decoding create response: %w", err)
	}

	if decoded.Id == "" {
		return nil, fmt.Errorf("create response carried no id: %s", created.Body)
	}

	return &CreatedLogStream{
		ID:         decoded.Id,
		Type:       LogStreamType(decoded.LogStreamType),
		HMACSecret: decoded.HmacSecret,
	}, nil
}

// GetLogStream fetches one log stream by id.
func GetLogStream(ctx context.Context, client *ClientWithResponses, id string) (*LogStream, error) {
	got, err := client.GetLogStreamWithResponse(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := CheckResponse(got); err != nil {
		return nil, err
	}

	return DecodeLogStream(got.Body)
}

// UpdateLogStream applies a patch that already carries its `logStreamType`.
func UpdateLogStream(ctx context.Context, client *ClientWithResponses, id string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding log stream update: %w", err)
	}

	return apierr.Retry(ctx, maxAttempts, baseDelay, func() error {
		updated, err := client.UpdateLogStreamWithBodyWithResponse(
			ctx, id, "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}

		return CheckResponse(updated)
	})
}

// DeleteLogStream removes a log stream.
func DeleteLogStream(ctx context.Context, client *ClientWithResponses, id string) error {
	return apierr.Retry(ctx, maxAttempts, baseDelay, func() error {
		deleted, err := client.DeleteLogStreamWithResponse(ctx, id)
		if err != nil {
			return err
		}

		return CheckResponse(deleted)
	})
}
