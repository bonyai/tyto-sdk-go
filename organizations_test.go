package tyto

import (
	"context"
	"testing"

	runtimev1 "github.com/bonyai/tyto-go/internal/gen/tyto/runtime/v1"
)

func TestListOrganizationsMapsFields(t *testing.T) {
	fake := &fakeTApi{organizations: []*runtimev1.TApiOrganization{
		{OrganizationId: "org-personal", Name: "personal", Personal: true, Role: "owner", CreatedAtUnixNanos: 1_000_000_000},
		{OrganizationId: "org-team", Name: "Acme", Personal: false, Role: "member", CreatedAtUnixNanos: 2_000_000_000},
	}}
	client := newBufconnClient(t, fake)

	organizations, err := client.ListOrganizations(context.Background())
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(organizations) != 2 {
		t.Fatalf("organizations = %d, want 2", len(organizations))
	}

	personal := organizations[0]
	if personal.ID != "org-personal" || !personal.Personal || personal.Role != "owner" {
		t.Errorf("personal org = %+v", personal)
	}
	if personal.CreatedAt.Unix() != 1 {
		t.Errorf("personal.CreatedAt = %v, want unix 1", personal.CreatedAt)
	}

	team := organizations[1]
	if team.ID != "org-team" || team.Personal || team.Role != "member" || team.Name != "Acme" {
		t.Errorf("team org = %+v", team)
	}
}

func TestListOrganizationsEmpty(t *testing.T) {
	fake := &fakeTApi{organizations: []*runtimev1.TApiOrganization{}}
	client := newBufconnClient(t, fake)

	organizations, err := client.ListOrganizations(context.Background())
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(organizations) != 0 {
		t.Fatalf("organizations = %v, want empty", organizations)
	}
}

func TestListOrganizationsSendsOrgContextHeader(t *testing.T) {
	fake := &fakeTApi{}
	client := newBufconnClient(t, fake, WithOrganizationID("org-123"))

	if _, err := client.ListOrganizations(context.Background()); err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}

	fake.mu.Lock()
	md := fake.lastMetadata
	fake.mu.Unlock()

	// ListOrganizations deliberately has no organization context of its own
	// -- it returns every organization the caller belongs to, not one scoped
	// by tenant -- but the interceptor is dial-time global and does not know
	// that, so the header still rides along. The server-side handler is what
	// ignores it (see compute's ListOrganizations, which reads only api_key).
	values := md.Get(organizationMetadataKey)
	if len(values) != 1 || values[0] != "org-123" {
		t.Errorf("bonya-organization-id metadata = %v, want [org-123]", values)
	}
}
