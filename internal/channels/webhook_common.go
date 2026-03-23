package channels

import (
	"net/http"
	"strings"
)

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					return t
				}
			}
		}
	}
	return ""
}

func jsonOK() WebhookHTTP {
	return WebhookHTTP{Status: http.StatusOK, ContentType: "application/json; charset=utf-8", Body: []byte(`{"ok":true}`)}
}
