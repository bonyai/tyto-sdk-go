package tyto

import "context"

// This file collects the flat, client-level sandbox methods --
// client.CreateSandbox(...) alongside client.Sandboxes.Create(...). Both
// spellings exist and both stay: some callers read better with the
// namespace (grouping every sandbox operation under one field is what makes
// sandbox.Files and sandbox.Sessions discoverable next to it), others read
// better as a verb straight off the client. Every method here is a thin,
// no-behavior delegation to the SandboxCollection method of the same
// operation, so there is exactly one implementation to keep correct --
// changing what "create a sandbox" does only ever means changing
// SandboxCollection.Create.
//
// This flattening stops at the client. sandbox.Files, sandbox.Sessions, and
// sandbox.Previews keep their namespaces: "sandbox.readFile" and
// "sandbox.createSession" were judged not to read better than
// "sandbox.Files.Read" and "sandbox.Sessions.Create", and flattening every
// namespace in the SDK would make Sandbox itself the thing with thirty
// methods on it.

// CreateSandbox is Sandboxes.Create.
func (c *Client) CreateSandbox(ctx context.Context, template string, opts ...CreateOptions) (*Sandbox, error) {
	return c.Sandboxes.Create(ctx, template, opts...)
}

// GetSandbox is Sandboxes.Get.
func (c *Client) GetSandbox(ctx context.Context, sandboxID string) (*Sandbox, error) {
	return c.Sandboxes.Get(ctx, sandboxID)
}

// GetSandboxByName is Sandboxes.GetByName.
func (c *Client) GetSandboxByName(ctx context.Context, name string) (*Sandbox, error) {
	return c.Sandboxes.GetByName(ctx, name)
}

// ListSandboxes is Sandboxes.List.
func (c *Client) ListSandboxes(ctx context.Context, opts ...ListOptions) ([]SandboxSummary, error) {
	return c.Sandboxes.List(ctx, opts...)
}

// DeleteSandbox is Sandboxes.Delete: a single id-only RPC, with no local
// handle to check for an already-known deletion. Sandbox.Delete is the
// handle-aware form, and is what a *Sandbox obtained from CreateSandbox or
// GetSandbox should generally use instead, so that a repeat call is a local
// no-op rather than a second RPC.
func (c *Client) DeleteSandbox(ctx context.Context, sandboxID string) (*DeleteResult, error) {
	return c.Sandboxes.Delete(ctx, sandboxID)
}

// ResumeSandbox is Sandboxes.Resume: a single id-only RPC, with no local
// handle to update afterward. Sandbox.Resume is the handle-aware form, and
// is what a *Sandbox should generally use instead, so that its exec
// capability and endpoint are refreshed for the next call rather than left
// stale.
func (c *Client) ResumeSandbox(ctx context.Context, sandboxID string, opts ...ResumeOptions) (*ResumeResult, error) {
	return c.Sandboxes.Resume(ctx, sandboxID, opts...)
}
