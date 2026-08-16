package tyto

import (
	"strings"
	"testing"
)

func TestNormalizeEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantURL    string
		wantTarget string
		wantErr    bool
	}{
		{name: "valid https", in: "https://compute.example.com", wantURL: "https://compute.example.com", wantTarget: "compute.example.com"},
		{name: "trailing slash trimmed", in: "https://compute.example.com/", wantURL: "https://compute.example.com", wantTarget: "compute.example.com"},
		{name: "trailing slash with path", in: "https://compute.example.com/api/", wantURL: "https://compute.example.com/api", wantTarget: "compute.example.com/api"},
		{name: "with port", in: "https://compute.example.com:8443", wantURL: "https://compute.example.com:8443", wantTarget: "compute.example.com:8443"},
		{name: "ipv6 host", in: "https://[::1]:8443", wantURL: "https://[::1]:8443", wantTarget: "[::1]:8443"},
		{name: "bare ipv4", in: "https://100.89.203.43", wantURL: "https://100.89.203.43", wantTarget: "100.89.203.43"},
		{name: "empty", in: "", wantErr: true},
		{name: "whitespace only", in: "   ", wantErr: true},
		{name: "http rejected", in: "http://compute.example.com", wantErr: true},
		{name: "userinfo rejected", in: "https://user:pass@compute.example.com", wantErr: true},
		{name: "query rejected", in: "https://compute.example.com?x=1", wantErr: true},
		{name: "fragment rejected", in: "https://compute.example.com#frag", wantErr: true},
		{name: "no host", in: "https://", wantErr: true},
		{name: "malformed port", in: "https://compute.example.com:notaport", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeEndpoint(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeEndpoint(%q) = %+v, want error", tt.in, got)
				}
				var invalidErr *InvalidRequestError
				if _, ok := err.(*InvalidRequestError); !ok {
					_ = invalidErr
					t.Fatalf("normalizeEndpoint(%q) error type = %T, want *InvalidRequestError", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeEndpoint(%q) unexpected error: %v", tt.in, err)
			}
			if got.url != tt.wantURL {
				t.Errorf("normalizeEndpoint(%q).url = %q, want %q", tt.in, got.url, tt.wantURL)
			}
			if got.target != tt.wantTarget {
				t.Errorf("normalizeEndpoint(%q).target = %q, want %q", tt.in, got.target, tt.wantTarget)
			}
		})
	}
}

func TestSanitizeMessageRedactsSecrets(t *testing.T) {
	secret := "byk_supersecretkey123"
	msg := "authentication failed for key " + secret + " on request"
	got := sanitizeMessage(msg, []string{secret})
	if want := "authentication failed for key [redacted] on request"; got != want {
		t.Errorf("sanitizeMessage() = %q, want %q", got, want)
	}
}

func TestSanitizeMessageRedactsPaths(t *testing.T) {
	msg := "failed to open /workspace/secrets/prod.env for reading"
	got := sanitizeMessage(msg, nil)
	if got == msg {
		t.Errorf("sanitizeMessage() did not redact a path-like substring: %q", got)
	}
	if !strings.Contains(got, "[redacted-path]") {
		t.Errorf("sanitizeMessage() = %q, want it to contain [redacted-path]", got)
	}
	if strings.Contains(got, "/workspace/secrets/prod.env") {
		t.Errorf("sanitizeMessage() = %q, still contains the raw path", got)
	}
}

func TestSanitizeMessageDoesNotRedactMidWordSlashes(t *testing.T) {
	// A slash preceded by a non-whitespace character (e.g. "a/b" inside a
	// word) is not a standalone path token and must be left alone, mirroring
	// the Python SDK's negative lookbehind (?<!\S).
	msg := "ratio was 3/4 today"
	got := sanitizeMessage(msg, nil)
	if got != msg {
		t.Errorf("sanitizeMessage() = %q, want unchanged %q", got, msg)
	}
}
