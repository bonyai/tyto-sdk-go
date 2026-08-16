package tyto

import (
	"context"
	"time"

	runtimev1 "github.com/bonyai/tyto-go/internal/gen/tyto/runtime/v1"
)

// ListOrganizations reports the organizations the client's API key's user
// belongs to, including the personal one every account has.
//
// Unlike Sandboxes.List this is not paginated: TApi returns every membership
// in one response, since a user's organization count is small and bounded by
// how many organizations they can join, not by anything the service stores
// per call.
func (c *Client) ListOrganizations(ctx context.Context) ([]Organization, error) {
	request := &runtimev1.TApiListOrganizationsRequest{ApiKey: c.apiKey}

	dl, err := startDeadline(c.timeout)
	if err != nil {
		return nil, err
	}
	attempts := 0
	backoff := 50 * time.Millisecond
	for {
		remaining, err := dl.remaining()
		if err != nil {
			return nil, MapRPCError(err, c.secrets())
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		client, tErr := c.tapiClient()
		if tErr != nil {
			cancel()
			return nil, tErr
		}
		response, callErr := client.ListOrganizations(callCtx, request)
		cancel()
		if callErr == nil {
			return organizationsFromResponse(response), nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return nil, MapRPCError(callErr, c.secrets())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

func organizationsFromResponse(response *runtimev1.TApiListOrganizationsResponse) []Organization {
	organizations := make([]Organization, 0, len(response.GetOrganizations()))
	for _, org := range response.GetOrganizations() {
		organizations = append(organizations, Organization{
			ID:        org.GetOrganizationId(),
			Name:      org.GetName(),
			Personal:  org.GetPersonal(),
			Role:      org.GetRole(),
			CreatedAt: time.Unix(0, org.GetCreatedAtUnixNanos()).UTC(),
		})
	}
	return organizations
}
