package tyto

import "strings"

// normalizeCommand converts an Exec command argument into an argv slice.
// Accepted inputs are string (executed as ["/bin/sh", "-c", command]) or
// []string (executed directly).
func normalizeCommand(command any) ([]string, error) {
	switch v := command.(type) {
	case string:
		if v == "" {
			return nil, &InvalidRequestError{BaseError{Msg: "command must not be empty"}}
		}
		return []string{"/bin/sh", "-c", v}, nil
	case []string:
		if len(v) == 0 {
			return nil, &InvalidRequestError{BaseError{Msg: "command must be a non-empty string sequence"}}
		}
		for _, arg := range v {
			if arg == "" {
				return nil, &InvalidRequestError{BaseError{Msg: "command must be a non-empty string sequence"}}
			}
		}
		out := make([]string, len(v))
		copy(out, v)
		return out, nil
	default:
		return nil, &InvalidRequestError{BaseError{Msg: "command must be a string or []string"}}
	}
}

func normalizeEnv(env map[string]string) (map[string]string, error) {
	if env == nil {
		return map[string]string{}, nil
	}
	normalized := make(map[string]string, len(env))
	for key, value := range env {
		if key == "" || strings.Contains(key, "=") || strings.Contains(key, "\x00") {
			return nil, &InvalidRequestError{BaseError{Msg: "env keys must be non-empty strings without '=' or NUL"}}
		}
		if strings.Contains(value, "\x00") {
			return nil, &InvalidRequestError{BaseError{Msg: "env values must be strings without NUL"}}
		}
		normalized[key] = value
	}
	return normalized, nil
}

func normalizeCwd(cwd string) (string, error) {
	if cwd == "" {
		return "", nil
	}
	if strings.Contains(cwd, "\x00") {
		return "", &InvalidRequestError{BaseError{Msg: "cwd must be a non-empty string without NUL"}}
	}
	return cwd, nil
}

// validateExecTTYOptions applies the buffered/streaming Exec TTY rules: cols
// and rows must be provided together, each 1-512, and both require tty=true.
func validateExecTTYOptions(tty bool, cols, rows int) (bool, int, int, error) {
	if !tty {
		if cols != 0 || rows != 0 {
			return false, 0, 0, &InvalidRequestError{BaseError{Msg: "tty dimensions require tty=true"}}
		}
		return false, 0, 0, nil
	}
	if cols == 0 && rows == 0 {
		return true, 0, 0, nil
	}
	c, err := validateDimension("cols", cols)
	if err != nil {
		return false, 0, 0, err
	}
	r, err := validateDimension("rows", rows)
	if err != nil {
		return false, 0, 0, err
	}
	return true, c, r, nil
}

// validateDimension validates a TTY cols/rows value, which must be 1-512.
func validateDimension(name string, value int) (int, error) {
	if value < 1 || value > 512 {
		return 0, &InvalidRequestError{BaseError{Msg: name + " must be a positive integer <= 512"}}
	}
	return value, nil
}

// validateRemotePath validates a remote filesystem path argument.
func validateRemotePath(path string) (string, error) {
	if path == "" || strings.Contains(path, "\x00") {
		return "", &InvalidRequestError{BaseError{Msg: "path must be a non-empty string without NUL"}}
	}
	return path, nil
}

const (
	sessionNameFirstChars = "abcdefghijklmnopqrstuvwxyz"
	sessionNameRestChars  = sessionNameFirstChars + "0123456789-"
	maxSessionNameLength  = 32
)

// validateSessionName validates a managed-session name against
// ^[a-z][a-z0-9-]{0,31}$.
func validateSessionName(name string) (string, error) {
	if name == "" {
		return "", &InvalidRequestError{BaseError{Msg: "session name must be a non-empty string"}}
	}
	if len(name) > maxSessionNameLength {
		return "", &InvalidRequestError{BaseError{Msg: "session name must be at most 32 characters"}}
	}
	if !strings.ContainsRune(sessionNameFirstChars, rune(name[0])) {
		return "", &InvalidRequestError{BaseError{Msg: "session name must match ^[a-z][a-z0-9-]{0,31}$"}}
	}
	for i := 1; i < len(name); i++ {
		if !strings.ContainsRune(sessionNameRestChars, rune(name[i])) {
			return "", &InvalidRequestError{BaseError{Msg: "session name must match ^[a-z][a-z0-9-]{0,31}$"}}
		}
	}
	return name, nil
}

// validateSessionCommand validates a managed-session command argv.
func validateSessionCommand(command []string) ([]string, error) {
	if len(command) == 0 {
		return nil, &InvalidRequestError{BaseError{Msg: "command must be a non-empty sequence of non-empty strings"}}
	}
	for _, arg := range command {
		if arg == "" {
			return nil, &InvalidRequestError{BaseError{Msg: "command must be a non-empty sequence of non-empty strings"}}
		}
	}
	out := make([]string, len(command))
	copy(out, command)
	return out, nil
}

// validateSessionDimension validates a managed-session cols/rows value,
// which is 0 (server default) or 1-512.
func validateSessionDimension(name string, value int) (int, error) {
	if value < 0 || value > 512 {
		return 0, &InvalidRequestError{BaseError{Msg: name + " must be a non-negative integer <= 512"}}
	}
	return value, nil
}
