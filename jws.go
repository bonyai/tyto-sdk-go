package tyto

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// jwsExpired sniffs a JWS's "exp" claim without verifying its signature --
// the SDK never trusts this value for authorization, only to decide whether
// a rejected capability is worth refreshing once. Any malformed input is
// treated as "not (provably) expired" so the SDK does not refresh
// speculatively on a token it cannot parse.
func jwsExpired(capability string) bool {
	parts := strings.Split(capability, ".")
	if len(parts) != 3 {
		return false
	}
	payload := parts[1]
	if rem := len(payload) % 4; rem != 0 {
		payload += strings.Repeat("=", 4-rem)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	var claims struct {
		Exp *float64 `json:"exp"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return false
	}
	if claims.Exp == nil {
		return false
	}
	expiry := time.Unix(0, int64(*claims.Exp*float64(time.Second)))
	return !time.Now().Before(expiry)
}
