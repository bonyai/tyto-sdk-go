package tyto

import "context"

// This file collects flat, client-level forms of operations that live on a
// Sandbox handle's Sessions, Previews, and snapshot methods -- for example
// client.CreateSession(ctx, sandboxID, ...) alongside
// sandbox.Sessions.Create(ctx, ...). Both spellings exist and both stay, for
// the same reason as flat.go's sandbox operations: some callers would rather
// call a verb with an id than fetch a handle first.
//
// Unlike the sandbox-collection operations in flat.go, every method here
// needs a resolved *Sandbox to call through -- sessions and previews are
// scoped to one sandbox's RPC surface, and snapshot creation checks the
// sandbox's last observed status. So each flat method here does a
// GetSandbox first and then delegates, which costs one extra round trip
// compared to already holding the handle. Call sandbox.Sessions.Create (or
// the equivalent) directly instead when a *Sandbox is already in hand, such
// as right after CreateSandbox.

// CreateSession is GetSandbox followed by Sandbox.Sessions.Create.
func (c *Client) CreateSession(ctx context.Context, sandboxID, name string, command []string, opts ...CreateSessionOptions) (SessionInfo, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return SessionInfo{}, err
	}
	return sandbox.Sessions.Create(ctx, name, command, opts...)
}

// ListSessions is GetSandbox followed by Sandbox.Sessions.List.
func (c *Client) ListSessions(ctx context.Context, sandboxID string) (SessionList, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return SessionList{}, err
	}
	return sandbox.Sessions.List(ctx)
}

// KillSession is GetSandbox followed by Sandbox.Sessions.Kill.
func (c *Client) KillSession(ctx context.Context, sandboxID, name string, opts ...KillOptions) (SessionInfo, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return SessionInfo{}, err
	}
	return sandbox.Sessions.Kill(ctx, name, opts...)
}

// AttachSession is GetSandbox followed by Sandbox.Sessions.Attach.
func (c *Client) AttachSession(ctx context.Context, sandboxID, name string, opts ...AttachOptions) (*SessionStream, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return sandbox.Sessions.Attach(ctx, name, opts...)
}

// CreatePreview is GetSandbox followed by Sandbox.Previews.Create.
func (c *Client) CreatePreview(ctx context.Context, sandboxID string, port int, opts ...CreatePreviewOptions) (Preview, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return Preview{}, err
	}
	return sandbox.Previews.Create(ctx, port, opts...)
}

// ListPreviews is GetSandbox followed by Sandbox.Previews.List.
func (c *Client) ListPreviews(ctx context.Context, sandboxID string) ([]Preview, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return sandbox.Previews.List(ctx)
}

// DeletePreview is GetSandbox followed by Sandbox.Previews.Delete.
func (c *Client) DeletePreview(ctx context.Context, sandboxID, previewID string) error {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}
	return sandbox.Previews.Delete(ctx, previewID)
}

// CreateSnapshot is GetSandbox followed by Sandbox.Snapshot.
func (c *Client) CreateSnapshot(ctx context.Context, sandboxID string, opts ...SnapshotOptions) (*Snapshot, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return sandbox.Snapshot(ctx, opts...)
}

// DeleteSnapshot is GetSandbox followed by Sandbox.DeleteSnapshot.
func (c *Client) DeleteSnapshot(ctx context.Context, sandboxID, snapshotID string) error {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}
	return sandbox.DeleteSnapshot(ctx, snapshotID)
}
