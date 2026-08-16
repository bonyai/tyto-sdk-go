package tyto

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// DefaultEndpoint is the SDK's default endpoint, used when neither
// WithEndpoint nor BONYA_ENDPOINT names one. Self-hosted deployments must set
// one of those explicitly.
const DefaultEndpoint = "https://api.tyto.run"

// organizationMetadataKey is the gRPC metadata carrier for org context. The
// REST surface names the same value X-Bonya-Organization-ID; omitting either
// resolves to the caller's personal organization.
const organizationMetadataKey = "bonya-organization-id"

// normalizedEndpoint is an endpoint that has passed validation, split into
// the full URL form (for display/identity) and the bare authority+path dial
// target grpc.NewClient expects.
type normalizedEndpoint struct {
	url    string
	target string
}

// normalizeEndpoint validates and canonicalizes an HTTPS endpoint URL.
//
// It rejects non-HTTPS URLs, URLs with userinfo, query strings, fragments,
// malformed ports, or no host, and trims a trailing slash.
func normalizeEndpoint(endpoint string) (normalizedEndpoint, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return normalizedEndpoint{}, &InvalidRequestError{BaseError{Msg: "endpoint is required"}}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return normalizedEndpoint{}, &InvalidRequestError{BaseError{Msg: "endpoint is invalid"}}
	}
	if parsed.Scheme != "https" {
		return normalizedEndpoint{}, &InvalidRequestError{BaseError{Msg: "endpoint must use https"}}
	}
	if parsed.User != nil {
		return normalizedEndpoint{}, &InvalidRequestError{BaseError{Msg: "endpoint must not include credentials"}}
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return normalizedEndpoint{}, &InvalidRequestError{BaseError{Msg: "endpoint must not include query strings or fragments"}}
	}
	host := parsed.Hostname()
	if host == "" {
		return normalizedEndpoint{}, &InvalidRequestError{BaseError{Msg: "endpoint requires a host"}}
	}
	portStr := parsed.Port()
	if portStr != "" {
		if _, err := strconv.ParseUint(portStr, 10, 16); err != nil {
			return normalizedEndpoint{}, &InvalidRequestError{BaseError{Msg: "endpoint has a malformed port"}}
		}
	}

	authority := host
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		authority = "[" + host + "]"
	}
	if portStr != "" {
		authority = authority + ":" + portStr
	}
	path := strings.TrimRight(parsed.Path, "/")

	full := url.URL{Scheme: "https", Host: authority, Path: path}
	return normalizedEndpoint{url: full.String(), target: authority + path}, nil
}

// orgContextInterceptor injects the configured organization id into
// outgoing gRPC metadata for every unary and streaming TApi call. It is
// applied once at dial time via grpc.WithChainUnaryInterceptor /
// grpc.WithChainStreamInterceptor rather than scattered per call site.
//
// It reads the organization id through a function rather than holding the
// value directly, because channels are cached per-endpoint (see
// Client.channel): an interceptor built once at dial time and holding a
// copied string would keep sending whatever organization was configured at
// the moment of the first call to that endpoint, silently ignoring any later
// SetOrganizationID. Reading through currentOrganizationID on every call
// makes the interceptor observe changes made after the channel was dialed.
type orgContextInterceptor struct {
	currentOrganizationID func() string
}

func (o orgContextInterceptor) unary() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if id := o.currentOrganizationID(); id != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, organizationMetadataKey, id)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func (o orgContextInterceptor) stream() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if id := o.currentOrganizationID(); id != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, organizationMetadataKey, id)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// pathRedactionPattern matches path-like substrings for sanitizeMessage, the
// same pattern the Python SDK uses:
// (?<!\S)/(?:[A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+
//
// Go's RE2 engine does not support lookbehind, so this is implemented with a
// leading boundary character class instead; see sanitizeMessage.
var pathSegmentPattern = regexp.MustCompile(`/(?:[A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+`)

// sanitizeMessage redacts known secrets and path-like substrings from a
// message before it is surfaced in a typed error.
func sanitizeMessage(message string, secrets []string) string {
	text := message
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return redactPaths(text)
}

// redactPaths replaces path-like substrings with "[redacted-path]", only
// where the match is not preceded by a non-whitespace character -- i.e. it
// starts at the beginning of the string or after whitespace, mirroring the
// Python SDK's negative lookbehind (?<!\S).
func redactPaths(text string) string {
	matches := pathSegmentPattern.FindAllStringIndex(text, -1)
	if matches == nil {
		return text
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if start > 0 && !isSpace(text[start-1]) {
			continue
		}
		b.WriteString(text[last:start])
		b.WriteString("[redacted-path]")
		last = end
	}
	b.WriteString(text[last:])
	return b.String()
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

// deadline tracks a wall-clock expiry derived from a timeout, mirroring the
// reference SDKs' Deadline helper for retry/backoff loops.
type deadline struct {
	expiresAt time.Time
}

// startDeadline begins a deadline timeout seconds from now. A non-positive
// timeout is an immediately-exhausted deadline.
func startDeadline(timeout time.Duration) (deadline, error) {
	if timeout <= 0 {
		return deadline{}, &TimeoutError{BaseError{Msg: "operation deadline exhausted"}}
	}
	return deadline{expiresAt: time.Now().Add(timeout)}, nil
}

// remaining returns the time left before the deadline, erroring once it has
// passed.
func (d deadline) remaining() (time.Duration, error) {
	remaining := time.Until(d.expiresAt)
	if remaining <= 0 {
		return 0, &TimeoutError{BaseError{Msg: "operation deadline exhausted"}}
	}
	return remaining, nil
}

// sleepWithDeadline sleeps for the shorter of d or the time remaining until
// the deadline expires.
func sleepWithDeadline(ctx context.Context, d time.Duration, dl deadline) {
	remaining := time.Until(dl.expiresAt)
	if remaining < 0 {
		remaining = 0
	}
	if d > remaining {
		d = remaining
	}
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
