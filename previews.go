package tyto

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	runtimev1 "buf.build/gen/go/bonya/tyto/protocolbuffers/go/tyto/runtime/v1"
)

const (
	minPreviewPort      = 1024
	maxPreviewPort      = 65535
	maxPreviewNameBytes = 80
	tokenQueryParam     = "bonya_token"
)

var authToProto = map[PreviewAuth]runtimev1.PreviewAuthMode{
	PreviewAuthToken:  runtimev1.PreviewAuthMode_PREVIEW_AUTH_MODE_TOKEN,
	PreviewAuthPublic: runtimev1.PreviewAuthMode_PREVIEW_AUTH_MODE_PUBLIC,
}

// protoToAuth maps an unrecognized mode to PreviewAuthToken rather than
// guessing open: a client from a future release must never describe a
// locked preview as public.
func protoToAuth(mode runtimev1.PreviewAuthMode) PreviewAuth {
	if mode == runtimev1.PreviewAuthMode_PREVIEW_AUTH_MODE_PUBLIC {
		return PreviewAuthPublic
	}
	return PreviewAuthToken
}

// CreatePreviewOptions configures Sandbox.CreatePreview.
type CreatePreviewOptions struct {
	Auth           PreviewAuth
	Name           string
	IdempotencyKey string
}

// CreatePreview publishes a preview URL for a guest port. These are TApi
// calls authenticated with the API key, not data-plane calls, so the
// capability-refresh wrapper that guards Exec and file operations does not
// apply here.
//
// On success the sandbox's stored capability is replaced with the one
// returned, because the preview scope is newer than the capability a
// sandbox was created with.
func (s *Sandbox) CreatePreview(ctx context.Context, port int, opts ...CreatePreviewOptions) (Preview, error) {
	if port < minPreviewPort || port > maxPreviewPort {
		return Preview{}, &InvalidRequestError{BaseError{Msg: "port must be between 1024 and 65535", SandboxID: s.ID}}
	}
	var o CreatePreviewOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	auth := o.Auth
	if auth == "" {
		auth = PreviewAuthToken
	}
	protoAuth, ok := authToProto[auth]
	if !ok {
		return Preview{}, &InvalidRequestError{BaseError{Msg: "auth must be a valid PreviewAuth", SandboxID: s.ID}}
	}
	if len(o.Name) > maxPreviewNameBytes {
		return Preview{}, &InvalidRequestError{BaseError{Msg: "name exceeds 80 bytes", SandboxID: s.ID}}
	}
	key := o.IdempotencyKey
	if key == "" {
		key = generatePreviewIdempotencyKey()
	}

	request := &runtimev1.TApiCreatePreviewRequest{
		ApiKey:         s.client.apiKey,
		SandboxId:      s.ID,
		Port:           uint32(port),
		AuthMode:       protoAuth,
		Name:           o.Name,
		IdempotencyKey: key,
	}
	dl, err := startDeadline(s.client.timeout)
	if err != nil {
		return Preview{}, err
	}
	remaining, err := dl.remaining()
	if err != nil {
		return Preview{}, s.mapPreviewError(err)
	}
	callCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	client, tErr := s.client.tapiClient()
	if tErr != nil {
		return Preview{}, tErr
	}
	response, callErr := client.CreatePreview(callCtx, request)
	if callErr != nil {
		return Preview{}, s.mapPreviewError(callErr)
	}

	if capability := response.GetCapabilityJws(); capability != "" {
		s.mu.Lock()
		s.capability = capability
		s.mu.Unlock()
	}
	if response.GetPreview().GetRecord().GetPreviewId() == "" {
		return Preview{}, &InvalidRequestError{BaseError{Msg: "CreatePreview response is missing the preview identity", SandboxID: s.ID, IdempotencyKey: key}}
	}
	return previewFromInfo(response.GetPreview()), nil
}

// ListPreviews returns every published preview for this sandbox.
func (s *Sandbox) ListPreviews(ctx context.Context) ([]Preview, error) {
	request := &runtimev1.TApiListPreviewsRequest{ApiKey: s.client.apiKey, SandboxId: s.ID}
	dl, err := startDeadline(s.client.timeout)
	if err != nil {
		return nil, err
	}
	remaining, err := dl.remaining()
	if err != nil {
		return nil, s.mapPreviewError(err)
	}
	callCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	client, tErr := s.client.tapiClient()
	if tErr != nil {
		return nil, tErr
	}
	response, callErr := client.ListPreviews(callCtx, request)
	if callErr != nil {
		return nil, s.mapPreviewError(callErr)
	}
	previews := make([]Preview, 0, len(response.GetPreviews()))
	for _, info := range response.GetPreviews() {
		previews = append(previews, previewFromInfo(info))
	}
	return previews, nil
}

// DeletePreview revokes a preview URL.
func (s *Sandbox) DeletePreview(ctx context.Context, previewID string) error {
	if previewID == "" {
		return &InvalidRequestError{BaseError{Msg: "preview id is required", SandboxID: s.ID}}
	}
	request := &runtimev1.TApiDeletePreviewRequest{ApiKey: s.client.apiKey, SandboxId: s.ID, PreviewId: previewID}
	dl, err := startDeadline(s.client.timeout)
	if err != nil {
		return err
	}
	remaining, err := dl.remaining()
	if err != nil {
		return s.mapPreviewError(err)
	}
	callCtx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	client, tErr := s.client.tapiClient()
	if tErr != nil {
		return tErr
	}
	if _, callErr := client.DeletePreview(callCtx, request); callErr != nil {
		return s.mapPreviewError(callErr)
	}
	return nil
}

// PreviewBrowserURL produces a single-use entry point that logs a browser
// into a token-mode preview: the gateway validates the token, trades it for
// a host-scoped HttpOnly cookie, and redirects to the same address without
// it.
//
// This must not be shared -- anyone who receives it holds the sandbox's
// data-plane capability until it expires. It errors on a public preview,
// which has no token to exchange.
func (s *Sandbox) PreviewBrowserURL(preview Preview) (string, error) {
	if preview.Auth == PreviewAuthPublic {
		return "", &InvalidRequestError{BaseError{Msg: "a public preview needs no token; use preview.URL", SandboxID: s.ID}}
	}
	_, capability := s.snapshotState()
	if capability == "" {
		return "", &InvalidRequestError{BaseError{Msg: "no capability is available for this sandbox", SandboxID: s.ID}}
	}
	separator := "?"
	if strings.Contains(preview.URL, "?") {
		separator = "&"
	}
	return preview.URL + separator + tokenQueryParam + "=" + capability, nil
}

func (s *Sandbox) mapPreviewError(err error) error {
	_, capability := s.snapshotState()
	return MapRPCError(err, s.client.secrets(capability), WithSandboxID(s.ID))
}

func previewFromInfo(info *runtimev1.PreviewInfo) Preview {
	record := info.GetRecord()
	return Preview{
		ID:        record.GetPreviewId(),
		SandboxID: record.GetSandboxId(),
		Port:      int(record.GetPort()),
		Auth:      protoToAuth(record.GetAuthMode()),
		Name:      record.GetName(),
		URL:       info.GetUrl(),
		CreatedAt: time.Unix(0, record.GetCreatedAtUnixNanos()).UTC(),
	}
}

func generatePreviewIdempotencyKey() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
