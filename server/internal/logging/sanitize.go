package logging

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const redacted = "***redacted***"

var (
	sensitiveKey    = regexp.MustCompile(`(?i)(authorization|cookie|password|passwd|secret|api[_-]?key|passkey|(^|[_-])token$|access[_-]?token|refresh[_-]?token|jwt|signature|(^|[_-])sig$)`)
	windowsPath     = regexp.MustCompile(`(?i)^[a-z]:[\\/]`)
	uncPath         = regexp.MustCompile(`^\\\\`)
	secretText      = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]+`)
	secretPair      = regexp.MustCompile(`(?i)\b(authorization|cookie|password|passwd|secret|api[_-]?key|passkey|access[_-]?token|refresh[_-]?token|jwt|sig|signature)\s*[:=]\s*[^\s,;]+`)
	windowsPathText = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]|\\\\)[^\s"']+`)
	unixPrivatePath = regexp.MustCompile(`(?:/home/|/Users/|/var/|/tmp/|/opt/|/srv/|/mnt/)[^\s"']+`)
)

func SanitizeJSON(line []byte) []byte {
	var event map[string]any
	if err := json.Unmarshal(line, &event); err != nil {
		return []byte(`{"level":"warn","message":"Malformed log event was discarded","module":"logging","component":"sanitizer"}` + "\n")
	}
	clean := sanitizeMap(event, 0)
	encoded, err := json.Marshal(clean)
	if err != nil {
		return []byte(`{"level":"warn","message":"Unencodable log event was discarded","module":"logging","component":"sanitizer"}` + "\n")
	}
	if len(encoded) > maxEventBytes {
		clean = compactOversizedEvent(clean)
		encoded, _ = json.Marshal(clean)
	}
	return append(encoded, '\n')
}

func compactOversizedEvent(event map[string]any) map[string]any {
	compact := map[string]any{"event_truncated": true}
	for _, key := range []string{"time", "timestamp", "level", "message", "module", "component", "operation", "operation_label", "plugin_id", "request_id", "user_id", "task_id", "library_id", "scan_run_id", "connection_id", "storage_id", "downloader_id", "status", "duration_ms"} {
		if value, ok := event[key]; ok {
			compact[key] = value
		}
	}
	return compact
}

func sanitizeMap(input map[string]any, depth int) map[string]any {
	out := make(map[string]any, min(len(input), 64))
	count := 0
	for key, value := range input {
		if count >= 64 {
			break
		}
		if sensitiveKey.MatchString(key) {
			out[key] = redacted
		} else {
			out[key] = sanitizeValue(key, value, depth+1)
		}
		count++
	}
	return out
}

func sanitizeValue(key string, value any, depth int) any {
	if depth > 6 {
		return "[truncated]"
	}
	switch typed := value.(type) {
	case string:
		return sanitizeString(key, typed)
	case map[string]any:
		return sanitizeMap(typed, depth)
	case []any:
		if len(typed) > 64 {
			typed = typed[:64]
		}
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = sanitizeValue(key, typed[i], depth+1)
		}
		return out
	default:
		return value
	}
}

func sanitizeString(key, value string) string {
	value = secretText.ReplaceAllString(value, `${1}`+redacted)
	isURL := false
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		isURL = true
		if parsed.User != nil {
			parsed.User = url.User(redacted)
		}
		query := parsed.Query()
		for name := range query {
			if sensitiveKey.MatchString(name) || strings.EqualFold(name, "exp") {
				query.Set(name, redacted)
			}
		}
		parsed.RawQuery = query.Encode()
		value = parsed.String()
	}
	if !isURL {
		value = secretPair.ReplaceAllStringFunc(value, func(pair string) string {
			separator := strings.IndexAny(pair, ":=")
			if separator < 0 {
				return redacted
			}
			return pair[:separator+1] + redacted
		})
		value = windowsPathText.ReplaceAllString(value, "[local-path-redacted]")
		value = unixPrivatePath.ReplaceAllString(value, "[local-path-redacted]")
	}
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "path") && (windowsPath.MatchString(value) || uncPath.MatchString(value) || strings.HasPrefix(value, "/")) {
		return "[local-path-redacted]"
	}
	if utf8.RuneCountInString(value) > 4096 {
		value = string([]rune(value)[:4096]) + "…[truncated]"
	}
	return value
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
