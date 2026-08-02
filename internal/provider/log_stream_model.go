package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/deployapi"
)

// logStreamModel maps the convex_log_stream schema. Exactly one sink pointer is
// non-nil, which the resource's ConfigValidators enforce.
type logStreamModel struct {
	DeploymentURL types.String `tfsdk:"deployment_url"`
	ID            types.String `tfsdk:"id"`
	Status        types.String `tfsdk:"status"`
	StatusReason  types.String `tfsdk:"status_reason"`

	Datadog              *datadogSinkModel  `tfsdk:"datadog"`
	Webhook              *webhookSinkModel  `tfsdk:"webhook"`
	Axiom                *axiomSinkModel    `tfsdk:"axiom"`
	Sentry               *sentrySinkModel   `tfsdk:"sentry"`
	PostHogLogs          *posthogLogsModel  `tfsdk:"posthog_logs"`
	PostHogErrorTracking *posthogErrorModel `tfsdk:"posthog_error_tracking"`
}

type datadogSinkModel struct {
	SiteLocation types.String `tfsdk:"site_location"`
	APIKey       types.String `tfsdk:"api_key"`
	Tags         types.List   `tfsdk:"tags"`
	Service      types.String `tfsdk:"service"`
	Topics       types.List   `tfsdk:"topics"`
}

type webhookSinkModel struct {
	URL        types.String `tfsdk:"url"`
	Format     types.String `tfsdk:"format"`
	Topics     types.List   `tfsdk:"topics"`
	HMACSecret types.String `tfsdk:"hmac_secret"`
}

type axiomSinkModel struct {
	APIKey      types.String `tfsdk:"api_key"`
	DatasetName types.String `tfsdk:"dataset_name"`
	Attributes  types.Map    `tfsdk:"attributes"`
	IngestURL   types.String `tfsdk:"ingest_url"`
	Topics      types.List   `tfsdk:"topics"`
}

type sentrySinkModel struct {
	DSN  types.String `tfsdk:"dsn"`
	Tags types.Map    `tfsdk:"tags"`
}

type posthogLogsModel struct {
	APIKey      types.String `tfsdk:"api_key"`
	Host        types.String `tfsdk:"host"`
	ServiceName types.String `tfsdk:"service_name"`
	Topics      types.List   `tfsdk:"topics"`
}

type posthogErrorModel struct {
	APIKey types.String `tfsdk:"api_key"`
	Host   types.String `tfsdk:"host"`
}

// hasSink reports whether a named sink block is set.
func (m *logStreamModel) hasSink(name string) bool {
	switch name {
	case "datadog":
		return m.Datadog != nil
	case "webhook":
		return m.Webhook != nil
	case "axiom":
		return m.Axiom != nil
	case "sentry":
		return m.Sentry != nil
	case "posthog_logs":
		return m.PostHogLogs != nil
	case "posthog_error_tracking":
		return m.PostHogErrorTracking != nil
	default:
		return false
	}
}

// sinkType returns the API discriminator for whichever sink is configured.
func (m *logStreamModel) sinkType() (deployapi.LogStreamType, error) {
	for _, name := range sinkAttributeNames {
		if m.hasSink(name) {
			return sinkTypeFor[name], nil
		}
	}

	return "", fmt.Errorf("no sink block is set")
}

// createPayload builds the create body, including its logStreamType. Payloads
// are plain maps rather than the generated union types, whose variants are
// positional -- see deployapi.LogStream.
func (m *logStreamModel) createPayload(ctx context.Context) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics

	streamType, err := m.sinkType()
	if err != nil {
		diags.AddError("No log stream sink configured", err.Error())
		return nil, diags
	}

	body := map[string]any{"logStreamType": string(streamType)}

	switch {
	case m.Datadog != nil:
		d := m.Datadog
		putString(body, "siteLocation", d.SiteLocation)
		putString(body, "ddApiKey", d.APIKey)
		putString(body, "service", d.Service)
		body["ddTags"] = stringsOrEmpty(ctx, d.Tags, &diags)
		putStrings(ctx, body, "topics", d.Topics, &diags)

	case m.Webhook != nil:
		w := m.Webhook
		putString(body, "url", w.URL)
		putString(body, "format", w.Format)
		putStrings(ctx, body, "topics", w.Topics, &diags)

	case m.Axiom != nil:
		a := m.Axiom
		putString(body, "apiKey", a.APIKey)
		putString(body, "datasetName", a.DatasetName)
		putString(body, "ingestUrl", a.IngestURL)
		body["attributes"] = axiomAttributes(ctx, a.Attributes, &diags)
		putStrings(ctx, body, "topics", a.Topics, &diags)

	case m.Sentry != nil:
		s := m.Sentry
		putString(body, "dsn", s.DSN)
		putMap(ctx, body, "tags", s.Tags, &diags)

	case m.PostHogLogs != nil:
		p := m.PostHogLogs
		putString(body, "apiKey", p.APIKey)
		putString(body, "host", p.Host)
		putString(body, "serviceName", p.ServiceName)
		putStrings(ctx, body, "topics", p.Topics, &diags)

	case m.PostHogErrorTracking != nil:
		p := m.PostHogErrorTracking
		putString(body, "apiKey", p.APIKey)
		putString(body, "host", p.Host)
	}

	return body, diags
}

// updatePayload builds the patch body. The update endpoint takes the same
// fields, all optional, so this is the create body minus anything unset.
func (m *logStreamModel) updatePayload(ctx context.Context) (map[string]any, diag.Diagnostics) {
	return m.createPayload(ctx)
}

// apply copies an API response onto the model, leaving credentials alone --
// the API never returns them, so what configuration supplied is all there is.
func (m *logStreamModel) apply(ctx context.Context, stream *deployapi.LogStream) diag.Diagnostics {
	var diags diag.Diagnostics

	m.ID = types.StringValue(stream.ID)
	m.Status = types.StringValue(stream.Status.Type)

	if stream.Status.Reason != "" {
		m.StatusReason = types.StringValue(stream.Status.Reason)
	} else {
		m.StatusReason = types.StringNull()
	}

	switch {
	case stream.Datadog != nil && m.Datadog != nil:
		c := stream.Datadog
		m.Datadog.SiteLocation = types.StringValue(string(c.SiteLocation))
		m.Datadog.Service = types.StringPointerValue(c.Service)
		m.Datadog.Tags = stringList(ctx, c.DdTags, &diags)
		m.Datadog.Topics = topicList(ctx, c.Topics, &diags)

	case stream.Webhook != nil && m.Webhook != nil:
		c := stream.Webhook
		m.Webhook.URL = types.StringValue(c.Url)
		m.Webhook.Format = types.StringValue(string(c.Format))
		m.Webhook.HMACSecret = types.StringValue(c.HmacSecret)
		m.Webhook.Topics = topicList(ctx, c.Topics, &diags)

	case stream.Axiom != nil && m.Axiom != nil:
		c := stream.Axiom
		m.Axiom.DatasetName = types.StringValue(c.DatasetName)
		m.Axiom.IngestURL = types.StringPointerValue(c.IngestUrl)
		m.Axiom.Attributes = attributeMap(ctx, c.Attributes, &diags)
		m.Axiom.Topics = topicList(ctx, c.Topics, &diags)

	case stream.Sentry != nil && m.Sentry != nil:
		c := stream.Sentry
		m.Sentry.Tags = optionalStringMap(ctx, c.Tags, &diags)

	case stream.PostHogLogs != nil && m.PostHogLogs != nil:
		c := stream.PostHogLogs
		m.PostHogLogs.Host = types.StringPointerValue(c.Host)
		m.PostHogLogs.ServiceName = types.StringPointerValue(c.ServiceName)
		m.PostHogLogs.Topics = topicList(ctx, c.Topics, &diags)

	case stream.PostHogErrorTracking != nil && m.PostHogErrorTracking != nil:
		c := stream.PostHogErrorTracking
		m.PostHogErrorTracking.Host = types.StringPointerValue(c.Host)

	default:
		// The remote stream is a different sink than configuration declares,
		// which means it was replaced outside Terraform.
		diags.AddError(
			"Log stream sink changed outside Terraform",
			fmt.Sprintf("Log stream %s is now a %q sink, but the configuration declares %s. "+
				"Remove it from state and re-import, or let Terraform replace it.",
				stream.ID, stream.Type, sinkNames(m)),
		)
	}

	return diags
}

// setUnknownComputedToNull resolves computed attributes that would otherwise
// stay unknown, so a partial state can still be written.
func (m *logStreamModel) setUnknownComputedToNull() {
	if m.Status.IsUnknown() {
		m.Status = types.StringNull()
	}

	if m.StatusReason.IsUnknown() {
		m.StatusReason = types.StringNull()
	}

	if m.Webhook != nil && m.Webhook.HMACSecret.IsUnknown() {
		m.Webhook.HMACSecret = types.StringNull()
	}
}

// --- conversion helpers -----------------------------------------------------

func putString(body map[string]any, key string, v types.String) {
	if isSet(v) {
		body[key] = v.ValueString()
	}
}

func putStrings(ctx context.Context, body map[string]any, key string, v types.List, diags *diag.Diagnostics) {
	if !isSet(v) {
		return
	}

	body[key] = stringsOrEmpty(ctx, v, diags)
}

func putMap(ctx context.Context, body map[string]any, key string, v types.Map, diags *diag.Diagnostics) {
	if !isSet(v) {
		return
	}

	var out map[string]string
	diags.Append(v.ElementsAs(ctx, &out, false)...)
	body[key] = out
}

func stringsOrEmpty(ctx context.Context, v types.List, diags *diag.Diagnostics) []string {
	out := []string{}

	if !isSet(v) {
		return out
	}

	diags.Append(v.ElementsAs(ctx, &out, false)...)

	return out
}

// axiomAttributes converts the map form used in configuration into the
// key/value list the API expects.
func axiomAttributes(ctx context.Context, v types.Map, diags *diag.Diagnostics) []map[string]string {
	out := []map[string]string{}

	if !isSet(v) {
		return out
	}

	var pairs map[string]string
	diags.Append(v.ElementsAs(ctx, &pairs, false)...)

	for k, value := range pairs {
		out = append(out, map[string]string{"key": k, "value": value})
	}

	return out
}

func stringList(ctx context.Context, values []string, diags *diag.Diagnostics) types.List {
	list, d := types.ListValueFrom(ctx, types.StringType, values)
	diags.Append(d...)

	return list
}

func topicList(ctx context.Context, topics *[]deployapi.LogTopic, diags *diag.Diagnostics) types.List {
	if topics == nil {
		return types.ListNull(types.StringType)
	}

	values := make([]string, 0, len(*topics))

	for _, t := range *topics {
		values = append(values, string(t))
	}

	return stringList(ctx, values, diags)
}

func attributeMap(ctx context.Context, attributes []deployapi.AxiomAttribute, diags *diag.Diagnostics) types.Map {
	pairs := make(map[string]string, len(attributes))

	for _, a := range attributes {
		pairs[a.Key] = a.Value
	}

	m, d := types.MapValueFrom(ctx, types.StringType, pairs)
	diags.Append(d...)

	return m
}

func optionalStringMap(ctx context.Context, values *map[string]string, diags *diag.Diagnostics) types.Map {
	if values == nil {
		return types.MapNull(types.StringType)
	}

	m, d := types.MapValueFrom(ctx, types.StringType, *values)
	diags.Append(d...)

	return m
}
