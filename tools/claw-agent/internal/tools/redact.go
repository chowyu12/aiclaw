package tools

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

var secretName = regexp.MustCompile(`(?i)(^|[-_])(token|password|passwd|secret|api[-_]?key|key|credentials?|authorization|cookie|private[-_]?key)([-_]|$)`)

// Capture known values once per command. Redact before previewing or retaining
// output, so pagination cannot retrieve the original secret later.
func (e *Env) commandRedactor() func(string, bool) string {
	values := append([]string(nil), e.SecretValues...)
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if secretName.MatchString(name) {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" && !seen[value] {
			pairs = append(pairs, value, "[REDACTED]")
			seen[value] = true
		}
	}
	replacer := strings.NewReplacer(pairs...)
	return func(text string, complete bool) string {
		if !complete {
			// A capture cap can split a secret. Do not retain its visible prefix.
			longest := 0
			for value := range seen {
				for size := min(len(value), len(text)); size > longest; size-- {
					if strings.HasSuffix(text, value[:size]) {
						longest = size
						break
					}
				}
			}
			if longest > 0 {
				text = text[:len(text)-longest] + "[REDACTED]"
			}
		}
		return replacer.Replace(text)
	}
}

// AddSecret accepts credential fields, excluding ordinary MCP environment and
// header values such as PATH or Content-Type.
func (e *Env) AddSecret(name, value string) {
	if !secretName.MatchString(name) {
		return
	}
	e.SecretValues = append(e.SecretValues, value)
	if _, token, ok := strings.Cut(value, " "); ok {
		e.SecretValues = append(e.SecretValues, token)
	}
}
