package provider

import (
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/srs/terraform-provider-convex/internal/convex"
)

// The same domain can be registered against both destinations, so the
// destination is part of the identity, not a detail.
func TestFindCustomDomainMatchesOnDestination(t *testing.T) {
	domains := []convex.PlatformCustomDomainResponse{
		{Domain: "api.example.com", RequestDestination: convex.ConvexCloud, CreationTime: 1},
		{Domain: "api.example.com", RequestDestination: convex.ConvexSite, CreationTime: 2},
		{Domain: "other.example.com", RequestDestination: convex.ConvexCloud, CreationTime: 3},
	}

	cloud, err := findCustomDomain(domains, "api.example.com", convex.ConvexCloud)
	if err != nil {
		t.Fatalf("findCustomDomain(convexCloud) error = %v", err)
	}
	if cloud.CreationTime != 1 {
		t.Errorf("convexCloud match has CreationTime %d, want 1", cloud.CreationTime)
	}

	site, err := findCustomDomain(domains, "api.example.com", convex.ConvexSite)
	if err != nil {
		t.Fatalf("findCustomDomain(convexSite) error = %v", err)
	}
	if site.CreationTime != 2 {
		t.Errorf("convexSite match has CreationTime %d, want 2", site.CreationTime)
	}
}

func TestFindCustomDomainNotFound(t *testing.T) {
	domains := []convex.PlatformCustomDomainResponse{
		{Domain: "api.example.com", RequestDestination: convex.ConvexCloud},
	}

	// Right domain, wrong destination.
	if _, err := findCustomDomain(domains, "api.example.com", convex.ConvexSite); !errors.Is(err, errCustomDomainNotFound) {
		t.Errorf("error = %v, want it to wrap errCustomDomainNotFound", err)
	}
	if _, err := findCustomDomain(domains, "absent.example.com", convex.ConvexCloud); !errors.Is(err, errCustomDomainNotFound) {
		t.Errorf("error = %v, want it to wrap errCustomDomainNotFound", err)
	}
	if _, err := findCustomDomain(nil, "api.example.com", convex.ConvexCloud); !errors.Is(err, errCustomDomainNotFound) {
		t.Errorf("error on empty list = %v, want it to wrap errCustomDomainNotFound", err)
	}
}

func TestApplyCustomDomain(t *testing.T) {
	verified := int64(1_700_000_500_000)

	tests := []struct {
		name             string
		verificationTime *int64
		wantVerified     types.Bool
		wantTime         types.Int64
	}{
		{
			name:             "unverified domain",
			verificationTime: nil,
			wantVerified:     types.BoolValue(false),
			wantTime:         types.Int64Null(),
		},
		{
			name:             "verified domain",
			verificationTime: &verified,
			wantVerified:     types.BoolValue(true),
			wantTime:         types.Int64Value(verified),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var model customDomainResourceModel
			applyCustomDomain(&model, &convex.PlatformCustomDomainResponse{
				Domain:             "api.example.com",
				DeploymentName:     "playful-otter-123",
				RequestDestination: convex.ConvexCloud,
				CreationTime:       1_700_000_000_000,
				VerificationTime:   tt.verificationTime,
			})

			if model.Verified != tt.wantVerified {
				t.Errorf("verified = %v, want %v", model.Verified, tt.wantVerified)
			}
			if model.VerificationTime != tt.wantTime {
				t.Errorf("verification_time = %v, want %v", model.VerificationTime, tt.wantTime)
			}
			if model.RequestDestination != types.StringValue("convexCloud") {
				t.Errorf("request_destination = %v, want convexCloud", model.RequestDestination)
			}
			if model.DeploymentName != types.StringValue("playful-otter-123") {
				t.Errorf("deployment_name = %v, want playful-otter-123", model.DeploymentName)
			}
		})
	}
}
