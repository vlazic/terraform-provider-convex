// Package convex contains the generated client for the Convex Management API
// (https://api.convex.dev/v1) along with thin hand-written helpers on top of it.
package convex

//go:generate go run github.com/srs/terraform-provider-convex/internal/tools/normalizespec ../../api/management-openapi.json ../../api/management-openapi.normalized.json
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=oapi-codegen.yaml ../../api/management-openapi.normalized.json
