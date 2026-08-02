package provider

import (
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/deployapi"
)

// logTopics are the event categories a stream can subscribe to. Generated from
// the LogTopic enum in the deployment OpenAPI document.
var logTopics = []string{
	"verification",
	"console",
	"function_execution",
	"exception",
	"audit_log",
	"scheduler_stats",
	"scheduled_job_lag",
	"current_storage_usage",
	"concurrency_stats",
	"storage_api_bandwidth",
	"log_stream_egress",
	"custom_audit",
}

// datadogSites are the Datadog deployment locations, from DatadogSiteLocation.
var datadogSites = []string{"US1", "US3", "US5", "EU", "US1_FED", "AP1"}

// webhookFormats are the payload encodings, from WebhookFormat.
var webhookFormats = []string{"json", "jsonl"}

// sinkAttributeNames are the nested blocks, one per sink type. Exactly one must
// be set; the resource's ConfigValidators enforce that from this list.
var sinkAttributeNames = []string{
	"datadog",
	"webhook",
	"axiom",
	"sentry",
	"posthog_logs",
	"posthog_error_tracking",
}

// sinkTypeFor maps a nested block name onto the API's discriminator.
var sinkTypeFor = map[string]deployapi.LogStreamType{
	"datadog":                deployapi.LogStreamDatadog,
	"webhook":                deployapi.LogStreamWebhook,
	"axiom":                  deployapi.LogStreamAxiom,
	"sentry":                 deployapi.LogStreamSentry,
	"posthog_logs":           deployapi.LogStreamPostHogLogs,
	"posthog_error_tracking": deployapi.LogStreamPostHogErrorTracking,
}

// topicsAttribute is shared by every sink that supports topic filtering.
func topicsAttribute() schema.ListAttribute {
	return schema.ListAttribute{
		Optional:    true,
		ElementType: types.StringType,
		MarkdownDescription: "Event topics to subscribe to. Omit to receive all topics, including ones " +
			"Convex adds later. One or more of: `" + joinBackticked(logTopics) + "`.",
		Validators: []validator.List{
			listvalidator.ValueStringsAre(stringvalidator.OneOf(logTopics...)),
			listvalidator.SizeAtLeast(1),
		},
	}
}

// replaceOnChange marks a sink block so that swapping sinks, or changing a
// field the API cannot patch, rebuilds the stream.
func replaceOnChange() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplace()}
}

func logStreamSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "Streams a Convex deployment's log events to an external destination, through " +
			"the [Deployment Platform API](https://docs.convex.dev/deployment-platform-api).\n\n" +
			"~> **Log streams require a Convex Pro plan.** On other plans the API rejects the request.\n\n" +
			"Exactly one sink block must be set. Switching to a different sink replaces the stream, since " +
			"Convex cannot convert one in place.\n\n" +
			"Convex starts a stream asynchronously, so `status` is usually `pending` right after the first " +
			"apply and becomes `active` on a later refresh. A stream that cannot start reports `failed` " +
			"together with a `status_reason`.\n\n" +
			"Credentials such as `api_key` and `dsn` are never returned by the API, so they are kept from " +
			"configuration and cannot be checked against the remote value.",
		Attributes: map[string]schema.Attribute{
			"deployment_url": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Backend URL of the deployment, as reported by " +
					"`convex_deployment.deployment_url`. Changing this forces a new resource.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						deploymentURLPattern,
						"must be an absolute http or https URL, for example https://playful-otter-123.convex.cloud",
					),
				},
				PlanModifiers: replaceOnChange(),
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier Convex assigned to the log stream.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Current state: `pending`, `restarting`, `active`, `failed` or " +
					"`deleting`. Set by Convex, so it changes outside Terraform.",
			},
			"status_reason": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Why the stream failed, when `status` is `failed`. Null otherwise.",
			},
		},
		Blocks: map[string]schema.Block{
			"datadog":                datadogBlock(),
			"webhook":                webhookBlock(),
			"axiom":                  axiomBlock(),
			"sentry":                 sentryBlock(),
			"posthog_logs":           posthogLogsBlock(),
			"posthog_error_tracking": posthogErrorTrackingBlock(),
		},
	}
}

func datadogBlock() schema.Block {
	return schema.SingleNestedBlock{
		MarkdownDescription: "Stream to Datadog.",
		Attributes: map[string]schema.Attribute{
			"site_location": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Datadog site the logs are sent to. One of `" +
					joinBackticked(datadogSites) + "`.",
				Validators: []validator.String{stringvalidator.OneOf(datadogSites...)},
			},
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Datadog API key. Never returned by the API, so it is kept from " +
					"configuration and stored in Terraform state.",
			},
			"tags": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Tags sent with every event via Datadog's `ddtags` field.",
			},
			"service": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Value for Datadog's `service` field.",
			},
			"topics": topicsAttribute(),
		},
	}
}

func webhookBlock() schema.Block {
	return schema.SingleNestedBlock{
		MarkdownDescription: "Stream to an arbitrary URL by POST.",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "URL that receives the POST requests.",
			},
			"format": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Payload encoding, `json` or `jsonl`.",
				Validators:          []validator.String{stringvalidator.OneOf(webhookFormats...)},
			},
			"topics": topicsAttribute(),
			"hmac_secret": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				MarkdownDescription: "Secret Convex signs each request with, so the receiver can verify " +
					"the payload. Generated at creation and stored in Terraform state.",
			},
		},
	}
}

func axiomBlock() schema.Block {
	return schema.SingleNestedBlock{
		MarkdownDescription: "Stream to an Axiom dataset.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Axiom API key. Never returned by the API, so it is kept from " +
					"configuration and stored in Terraform state.",
			},
			"dataset_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Axiom dataset that receives the events.",
			},
			"attributes": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Extra key/value fields attached to every event sent to Axiom.",
			},
			"ingest_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Override for Axiom's ingest endpoint.",
			},
			"topics": topicsAttribute(),
		},
	}
}

func sentryBlock() schema.Block {
	return schema.SingleNestedBlock{
		MarkdownDescription: "Route exceptions to Sentry.",
		Attributes: map[string]schema.Attribute{
			"dsn": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Sentry DSN. Never returned by the API, so it is kept from " +
					"configuration and stored in Terraform state.",
			},
			"tags": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Tags added to every event routed to Sentry.",
			},
		},
	}
}

func posthogLogsBlock() schema.Block {
	return schema.SingleNestedBlock{
		MarkdownDescription: "Stream logs to PostHog.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "PostHog project API key. Never returned by the API, so it is kept " +
					"from configuration and stored in Terraform state.",
			},
			"host": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "PostHog host, for self-hosted or non-default regions.",
			},
			"service_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Service name reported with each event.",
			},
			"topics": topicsAttribute(),
		},
	}
}

func posthogErrorTrackingBlock() schema.Block {
	return schema.SingleNestedBlock{
		MarkdownDescription: "Route exceptions to PostHog error tracking.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "PostHog project API key. Never returned by the API, so it is kept " +
					"from configuration and stored in Terraform state.",
			},
			"host": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "PostHog host, for self-hosted or non-default regions.",
			},
		},
	}
}

func joinBackticked(values []string) string {
	out := ""

	for i, v := range values {
		if i > 0 {
			out += "`, `"
		}

		out += v
	}

	return out
}
