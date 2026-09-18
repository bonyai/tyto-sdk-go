package tyto

import "context"

// This file collects flat, client-level forms of operations that live on a
// Sandbox handle -- for example client.CreateSession(ctx, sandboxID, ...)
// alongside sandbox.CreateSession(ctx, ...). Both spellings exist and both
// stay, for the same reason as this SDK's other flat sandbox operations:
// some callers would rather call a verb with an id than fetch a handle
// first.
//
// Every method here needs a resolved *Sandbox to call through -- sessions
// and previews are scoped to one sandbox's RPC surface, and snapshot
// creation checks the sandbox's last observed status. So each flat method
// here does a GetSandbox first and then delegates, which costs one extra
// round trip compared to already holding the handle. Call
// sandbox.CreateSession (or the equivalent) directly instead when a
// *Sandbox is already in hand, such as right after CreateSandbox.

// CreateSession is GetSandbox followed by Sandbox.CreateSession.
func (c *Client) CreateSession(ctx context.Context, sandboxID, name string, command []string, opts ...CreateSessionOptions) (SessionInfo, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return SessionInfo{}, err
	}
	return sandbox.CreateSession(ctx, name, command, opts...)
}

// ListSessions is GetSandbox followed by Sandbox.ListSessions.
func (c *Client) ListSessions(ctx context.Context, sandboxID string) (SessionList, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return SessionList{}, err
	}
	return sandbox.ListSessions(ctx)
}

// KillSession is GetSandbox followed by Sandbox.KillSession.
func (c *Client) KillSession(ctx context.Context, sandboxID, name string, opts ...KillOptions) (SessionInfo, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return SessionInfo{}, err
	}
	return sandbox.KillSession(ctx, name, opts...)
}

// AttachSession is GetSandbox followed by Sandbox.AttachSession.
func (c *Client) AttachSession(ctx context.Context, sandboxID, name string, opts ...AttachOptions) (*SessionStream, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return sandbox.AttachSession(ctx, name, opts...)
}

// CreatePreview is GetSandbox followed by Sandbox.CreatePreview.
func (c *Client) CreatePreview(ctx context.Context, sandboxID string, port int, opts ...CreatePreviewOptions) (Preview, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return Preview{}, err
	}
	return sandbox.CreatePreview(ctx, port, opts...)
}

// ListPreviews is GetSandbox followed by Sandbox.ListPreviews.
func (c *Client) ListPreviews(ctx context.Context, sandboxID string) ([]Preview, error) {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	return sandbox.ListPreviews(ctx)
}

// DeletePreview is GetSandbox followed by Sandbox.DeletePreview.
func (c *Client) DeletePreview(ctx context.Context, sandboxID, previewID string) error {
	sandbox, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		return err
	}
	return sandbox.DeletePreview(ctx, previewID)
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
