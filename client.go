// Package tyto is a Go SDK for the Tyto Compute API.
//
// Conceptual SDK version: v1.0.0.
package tyto

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	runtimev1 "github.com/bonyai/tyto-go/internal/gen/tyto/runtime/v1"
)

const defaultTimeout = 30 * time.Second
const defaultMaxRetries = 2
const defaultFilesystemReadLimit = 64 * 1024 * 1024

// Client is a connection to the Tyto Compute API. Create one with NewClient.
type Client struct {
	apiKey              string
	endpoint            normalizedEndpoint
	organizationID      string
	timeout             time.Duration
	maxRetries          int
	filesystemReadLimit int64

	mu       sync.Mutex
	closed   bool
	channels map[string]*grpc.ClientConn
	creds    credentials.TransportCredentials

	// Sandboxes is the entry point for creating, fetching, and listing sandboxes.
	Sandboxes *SandboxCollection
}

// Option configures a Client constructed by NewClient.
type Option func(*clientConfig)

type clientConfig struct {
	apiKey              *string
	endpoint            *string
	caBundle            *string
	organizationID      *string
	timeout             *time.Duration
	maxRetries          *int
	filesystemReadLimit *int64
}

// WithAPIKey sets the API key. Defaults to the BONYA_API_KEY environment variable.
func WithAPIKey(key string) Option { return func(c *clientConfig) { c.apiKey = &key } }

// WithEndpoint sets the gRPC endpoint. Defaults to BONYA_ENDPOINT, then DefaultEndpoint.
func WithEndpoint(endpoint string) Option { return func(c *clientConfig) { c.endpoint = &endpoint } }

// WithCABundle sets a PEM bundle path used for private development CAs.
// Defaults to the BONYA_CA_BUNDLE environment variable.
func WithCABundle(path string) Option { return func(c *clientConfig) { c.caBundle = &path } }

// WithOrganizationID selects which organization the client's calls act on.
// Defaults to the BONYA_ORGANIZATION_ID environment variable, and then the
// caller's personal organization when neither is set. An explicitly empty
// value is an error rather than a silent fallback.
func WithOrganizationID(id string) Option { return func(c *clientConfig) { c.organizationID = &id } }

// WithTimeout sets the default per-operation deadline. It must be positive.
func WithTimeout(timeout time.Duration) Option {
	return func(c *clientConfig) { c.timeout = &timeout }
}

// WithMaxRetries sets how many times the SDK retries a retryable
// control-plane operation. It must be non-negative.
func WithMaxRetries(maxRetries int) Option {
	return func(c *clientConfig) { c.maxRetries = &maxRetries }
}

// WithFilesystemReadLimit caps bytes buffered by SandboxFiles.Read. It must
// be non-negative and defaults to 64 MiB.
func WithFilesystemReadLimit(limit int64) Option {
	return func(c *clientConfig) { c.filesystemReadLimit = &limit }
}

// NewClient constructs a Client from options and environment variables.
func NewClient(opts ...Option) (*Client, error) {
	var cfg clientConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	apiKey := stringOrEnv(cfg.apiKey, "BONYA_API_KEY")
	if apiKey == "" {
		return nil, &InvalidRequestError{BaseError{Msg: "api_key is required"}}
	}

	endpointValue := stringOrEnv(cfg.endpoint, "BONYA_ENDPOINT")
	if endpointValue == "" {
		endpointValue = DefaultEndpoint
	}
	endpoint, err := normalizeEndpoint(endpointValue)
	if err != nil {
		return nil, err
	}

	caBundle := stringOrEnv(cfg.caBundle, "BONYA_CA_BUNDLE")

	organizationID, err := resolveOrganizationID(cfg.organizationID)
	if err != nil {
		return nil, err
	}

	timeout := defaultTimeout
	if cfg.timeout != nil {
		timeout = *cfg.timeout
	}
	if timeout <= 0 {
		return nil, &InvalidRequestError{BaseError{Msg: "timeout must be positive"}}
	}

	maxRetries := defaultMaxRetries
	if cfg.maxRetries != nil {
		maxRetries = *cfg.maxRetries
	}
	if maxRetries < 0 {
		return nil, &InvalidRequestError{BaseError{Msg: "max_retries must be non-negative"}}
	}

	filesystemReadLimit := int64(defaultFilesystemReadLimit)
	if cfg.filesystemReadLimit != nil {
		filesystemReadLimit = *cfg.filesystemReadLimit
	}
	if filesystemReadLimit < 0 {
		return nil, &InvalidRequestError{BaseError{Msg: "filesystem_read_limit must be a non-negative integer"}}
	}

	creds, err := channelCredentials(caBundle)
	if err != nil {
		return nil, err
	}

	client := &Client{
		apiKey:              apiKey,
		endpoint:            endpoint,
		organizationID:      organizationID,
		timeout:             timeout,
		maxRetries:          maxRetries,
		filesystemReadLimit: filesystemReadLimit,
		channels:            make(map[string]*grpc.ClientConn),
		creds:               creds,
	}
	client.Sandboxes = &SandboxCollection{client: client}
	return client, nil
}

// stringOrEnv returns the explicit value if set, otherwise the named
// environment variable (which may be empty/unset).
func stringOrEnv(explicit *string, envVar string) string {
	if explicit != nil {
		return *explicit
	}
	return os.Getenv(envVar)
}

// resolveOrganizationID resolves org context from the option, then the
// environment. An explicitly supplied empty value is an error rather than a
// silent fallback to the personal organization.
func resolveOrganizationID(explicit *string) (string, error) {
	var value *string
	if explicit != nil {
		value = explicit
	} else if v, ok := os.LookupEnv("BONYA_ORGANIZATION_ID"); ok {
		value = &v
	}
	if value == nil {
		return "", nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return "", &InvalidRequestError{BaseError{Msg: "organization_id must be a non-empty string"}}
	}
	return trimmed, nil
}

func channelCredentials(caBundle string) (credentials.TransportCredentials, error) {
	if caBundle == "" {
		return credentials.NewTLS(&tls.Config{}), nil
	}
	pemBytes, err := os.ReadFile(caBundle)
	if err != nil {
		return nil, &InvalidRequestError{BaseError{Msg: "ca_bundle could not be read"}}
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, &InvalidRequestError{BaseError{Msg: "ca_bundle could not be parsed"}}
	}
	return credentials.NewTLS(&tls.Config{RootCAs: pool}), nil
}

// Close closes cached gRPC channels. It is idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	channels := make([]*grpc.ClientConn, 0, len(c.channels))
	for _, ch := range c.channels {
		channels = append(channels, ch)
	}
	c.channels = nil
	c.mu.Unlock()

	var firstErr error
	for _, ch := range channels {
		if err := ch.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// OrganizationID returns the org context this client currently sends, or ""
// for the personal org.
func (c *Client) OrganizationID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.organizationID
}

// SetOrganizationID changes which organization this client's calls act on.
// It takes effect immediately, including on gRPC channels already dialed and
// cached by earlier calls -- see the note on orgContextInterceptor for why
// that is safe. An empty id is an error rather than a silent fallback to the
// personal organization, matching WithOrganizationID and the constructor's
// BONYA_ORGANIZATION_ID handling.
func (c *Client) SetOrganizationID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return &InvalidRequestError{BaseError{Msg: "organization_id must be a non-empty string"}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.organizationID = id
	return nil
}

// channel returns (creating if necessary) a cached gRPC connection to the
// given normalized endpoint.
func (c *Client) channel(endpoint normalizedEndpoint) (*grpc.ClientConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, &InvalidRequestError{BaseError{Msg: "client is closed"}}
	}
	if ch, ok := c.channels[endpoint.url]; ok {
		return ch, nil
	}
	interceptor := orgContextInterceptor{currentOrganizationID: c.OrganizationID}
	ch, err := grpc.NewClient(
		endpoint.target,
		grpc.WithTransportCredentials(c.creds),
		grpc.WithChainUnaryInterceptor(interceptor.unary()),
		grpc.WithChainStreamInterceptor(interceptor.stream()),
	)
	if err != nil {
		return nil, &ConnectionError{BaseError{Msg: sanitizeMessage(err.Error(), c.secrets())}}
	}
	c.channels[endpoint.url] = ch
	return ch, nil
}

// tapiClient returns a TApiServiceClient bound to the control-plane channel.
// Organization context is injected by the interceptor registered at dial
// time in channel(), not per call site.
func (c *Client) tapiClient() (runtimev1.TApiServiceClient, error) {
	ch, err := c.channel(c.endpoint)
	if err != nil {
		return nil, err
	}
	return runtimev1.NewTApiServiceClient(ch), nil
}

// guestClient returns a GuestServiceClient bound to a sandbox's private exec
// endpoint.
func (c *Client) guestClient(endpoint string) (runtimev1.GuestServiceClient, error) {
	normalized, err := normalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	ch, err := c.channel(normalized)
	if err != nil {
		return nil, err
	}
	return runtimev1.NewGuestServiceClient(ch), nil
}

// secrets returns the values this client should redact from error messages:
// the API key plus any extra values supplied by the caller (e.g. a capability).
func (c *Client) secrets(extra ...string) []string {
	values := make([]string, 0, len(extra)+1)
	if c.apiKey != "" {
		values = append(values, c.apiKey)
	}
	for _, v := range extra {
		if v != "" {
			values = append(values, v)
		}
	}
	return values
}

// withOutgoingMetadata is a small helper retained for future call sites that
// need to append metadata beyond what the org-context interceptor sends.
func withOutgoingMetadata(ctx context.Context, pairs ...string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}
