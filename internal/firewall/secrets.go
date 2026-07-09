package firewall

import (
	"regexp"
	"strings"
)

// Порт src/signals/secrets.ts из Cerberus.
// High-confidence structured secret patterns. Без entropy fallback — FP слишком высок для API traffic.

type SecretPattern struct {
	Type       string
	Re         *regexp.Regexp
	Confidence float64
	ValueGroup int
}

var secretPatterns = []SecretPattern{
	{Type: "aws-access-key", Re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), Confidence: 0.98},
	{Type: "github-token", Re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`), Confidence: 0.98},
	{Type: "openai-key", Re: regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9]{20,}\b`), Confidence: 0.97},
	{Type: "slack-token", Re: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`), Confidence: 0.95},
	{Type: "google-api-key", Re: regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`), Confidence: 0.95},
	{Type: "private-key", Re: regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----`), Confidence: 0.9},
	{Type: "jwt", Re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), Confidence: 0.9},
	{Type: "generic-secret-assignment", Re: regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|password|passwd|token)\b\s*[:=]\s*['"]?([A-Za-z0-9_\-.]{12,})`), Confidence: 0.85, ValueGroup: 1},
}

var pemBlockRe = regexp.MustCompile(`-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z ]+ )?PRIVATE KEY-----`)

type FoundSecret struct {
	Value      string
	Type       string
	Confidence float64
}

// DetectSecrets находит structured секреты в тексте.
func DetectSecrets(text string) []FoundSecret {
	var out []FoundSecret
	for _, p := range secretPatterns {
		matches := p.Re.FindAllStringSubmatchIndex(text, -1)
		for _, m := range matches {
			var value string
			if p.ValueGroup > 0 && m[2*p.ValueGroup] >= 0 {
				value = text[m[2*p.ValueGroup]:m[2*p.ValueGroup+1]]
			} else {
				value = text[m[0]:m[1]]
			}
			if len(value) >= 8 {
				out = append(out, FoundSecret{Value: value, Type: p.Type, Confidence: p.Confidence})
			}
		}
	}
	return out
}

// RedactSecrets заменяет найденные секреты на [REDACTED:type], не логгируя сам value.
func RedactSecrets(text string) (string, []string) {
	types := map[string]bool{}
	count := 0

	out := pemBlockRe.ReplaceAllStringFunc(text, func(_ string) string {
		count++
		types["private-key"] = true
		return "[REDACTED:private-key]"
	})

	found := DetectSecrets(out)
	// Сортируем по убыванию длины — избегаем частичных перекрытий
	for i := 0; i < len(found); i++ {
		for j := i + 1; j < len(found); j++ {
			if len(found[j].Value) > len(found[i].Value) {
				found[i], found[j] = found[j], found[i]
			}
		}
	}

	for _, f := range found {
		if f.Type == "private-key" || len(f.Value) < 8 || !strings.Contains(out, f.Value) {
			continue
		}
		out = strings.ReplaceAll(out, f.Value, "[REDACTED:"+f.Type+"]")
		count++
		types[f.Type] = true
	}

	var typeList []string
	for t := range types {
		typeList = append(typeList, t)
	}
	return out, typeList
}
