// Package convex contains the generated client for the Convex Management API
// (https://api.convex.dev/v1) along with thin hand-written helpers on top of it.
package convex

//go:generate go run github.com/srs/terraform-provider-convex/internal/tools/normalizespec ../../api/openapi.json ../../api/openapi.normalized.json
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=oapi-codegen.yaml ../../api/openapi.normalized.json
