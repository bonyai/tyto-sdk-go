package tyto

import (
	"context"
	"time"

	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

// ListTemplates reports the deployment's immutable template catalog: every
// template_id/version binding Sandboxes.Create and RunJob will accept, plus
// which version each template_id resolves to when a caller omits version.
//
// Like ListOrganizations this is not paginated: the catalog is loaded once
// at startup and is the same for every tenant.
func (c *Client) ListTemplates(ctx context.Context) ([]Template, error) {
	request := &runtimev1.TApiListTemplatesRequest{ApiKey: c.apiKey}

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
		response, callErr := client.ListTemplates(callCtx, request)
		cancel()
		if callErr == nil {
			return templatesFromResponse(response), nil
		}
		if !IsRetryable(callErr) || attempts >= c.maxRetries {
			return nil, MapRPCError(callErr, c.secrets())
		}
		attempts++
		sleepWithDeadline(ctx, backoff, dl)
		backoff = minDuration(backoff*2, 500*time.Millisecond)
	}
}

func templatesFromResponse(response *runtimev1.TApiListTemplatesResponse) []Template {
	templates := make([]Template, 0, len(response.GetTemplates()))
	for _, t := range response.GetTemplates() {
		templates = append(templates, Template{
			ID:        t.GetTemplateId(),
			Version:   t.GetVersion(),
			Digest:    t.GetDigest(),
			IsDefault: t.GetIsDefault(),
		})
	}
	return templates
}
