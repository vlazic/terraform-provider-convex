// Package deployapi contains the generated client for the Convex Deployment
// Platform API, along with thin hand-written helpers on top of it.
//
// This is a different API from the one in internal/convex. The management API
// provisions projects and deployments and is addressed at a single host; this
// one configures an individual deployment, is addressed at that deployment's
// own URL, and takes a `Convex ` authorization prefix rather than `Bearer `.
// The same team access token authenticates both.
//
// The upstream document is not published alongside the API, so it is vendored
// from the convex-backend repository -- see the Makefile's fetch-spec target.
package deployapi

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=oapi-codegen.yaml ../../api/deployment-openapi.json
