package firewall

import (
	"encoding/json"
	"strings"
)

type Action int

const (
	Allow Action = iota
	Block
	Redact
)

type Verdict struct {
	Action      Action
	Reasons     []string
	SecretTypes []string
	Body        []byte // мутированное тело при Redact
}

// extractTextFromMessages извлекает текстовый content из messages запроса.
func extractTextFromMessages(body []byte) string {
	var req struct {
		Messages []struct {
			Content interface{} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, msg := range req.Messages {
		switch v := msg.Content.(type) {
		case string:
			sb.WriteString(v)
			sb.WriteString("\n")
		case []interface{}:
			for _, part := range v {
				if m, ok := part.(map[string]interface{}); ok {
					if t, ok := m["type"].(string); ok && t == "text" {
						if text, ok := m["text"].(string); ok {
							sb.WriteString(text)
							sb.WriteString("\n")
						}
					}
				}
			}
		}
	}
	return sb.String()
}

// extractTextFromResponse извлекает текстовый content из ответа API (non-stream).
func extractTextFromResponse(body []byte) string {
	var resp struct {
		Choices []struct {
			Message struct {
				Content interface{} `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, ch := range resp.Choices {
		switch v := ch.Message.Content.(type) {
		case string:
			sb.WriteString(v)
			sb.WriteString("\n")
		case []interface{}:
			for _, part := range v {
				if m, ok := part.(map[string]interface{}); ok {
					if t, ok := m["type"].(string); ok && t == "text" {
						if text, ok := m["text"].(string); ok {
							sb.WriteString(text)
							sb.WriteString("\n")
						}
					}
				}
			}
		}
	}
	return sb.String()
}

// InspectRequest сканирует тело запроса (messages content) на секреты.
func InspectRequest(body []byte, block, redact bool) Verdict {
	text := extractTextFromMessages(body)
	secrets := DetectSecrets(text)
	if len(secrets) == 0 {
		return Verdict{Action: Allow}
	}

	var types []string
	for _, s := range secrets {
		types = append(types, s.Type)
	}

	if redact {
		fullText := string(body)
		redacted, usedTypes := RedactSecrets(fullText)
		return Verdict{
			Action:      Redact,
			Reasons:     []string{"secrets found and redacted in request"},
			SecretTypes: usedTypes,
			Body:        []byte(redacted),
		}
	}

	if block {
		return Verdict{
			Action:      Block,
			Reasons:     []string{"secrets detected in request"},
			SecretTypes: types,
		}
	}

	return Verdict{
		Action:      Allow,
		Reasons:     []string{"secrets detected (log-only mode)"},
		SecretTypes: types,
	}
}

// InspectResponse сканирует тело ответа (non-stream) на injection, опасные команды, секреты.
func InspectResponse(body []byte, block, redact bool) Verdict {
	text := extractTextFromResponse(body)

	var reasons []string
	var action Action = Allow

	// Injection
	if score, label := ClassifyInjection(text); score == 1 && label == "injection" {
		reasons = append(reasons, "prompt injection detected in response")
		if block {
			action = Block
		}
	}

	// Опасные команды
	if ClassifyCommand(text) {
		reasons = append(reasons, "dangerous command detected in response")
		if block {
			action = Block
		}
	}

	// Секреты — редачим если redact, иначе блокируем если block
	secrets := DetectSecrets(text)
	if len(secrets) > 0 {
		var types []string
		for _, s := range secrets {
			types = append(types, s.Type)
		}
		if redact {
			redacted, usedTypes := RedactSecrets(string(body))
			if action != Block {
				action = Redact
			}
			reasons = append(reasons, "secrets redacted in response")
			return Verdict{
				Action:      action,
				Reasons:     append(reasons, "secrets found in response"),
				SecretTypes: usedTypes,
				Body:        []byte(redacted),
			}
		}
		reasons = append(reasons, "secrets detected in response")
		if block && action != Block {
			action = Block
		}
		_ = types
	}

	if action == Allow && len(reasons) == 0 {
		return Verdict{Action: Allow}
	}

	return Verdict{Action: action, Reasons: reasons}
}

// StreamScanner — сканер для streaming SSE deltas.
// Накапливает content, сканирует на секреты инлайн, injection/команды логирует.
type StreamScanner struct {
	buffer      strings.Builder
	redact      bool
	redacted    bool
	injected    bool
	commands    bool
	secretTypes []string
}

func NewStreamScanner(redact bool) *StreamScanner {
	return &StreamScanner{redact: redact}
}

// ScanLine обрабатывает SSE строку, возвращает potentially modified line.
func (s *StreamScanner) ScanLine(line []byte) []byte {
	lineStr := string(line)
	if !strings.HasPrefix(lineStr, "data:") {
		return line
	}
	dataJSON := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
	if dataJSON == "[DONE]" || dataJSON == "" {
		return line
	}

	var chunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &chunk); err != nil {
		return line
	}

	var deltaContent string
	for _, ch := range chunk.Choices {
		if ch.Delta.Content != "" {
			s.buffer.WriteString(ch.Delta.Content)
			deltaContent = ch.Delta.Content
		}
	}

	// Проверяем накопленный буфер на injection/команды (log-only)
	text := s.buffer.String()
	if !s.injected {
		if score, _ := ClassifyInjection(text); score == 1 {
			s.injected = true
		}
	}
	if !s.commands {
		if ClassifyCommand(text) {
			s.commands = true
		}
	}

	// Секреты — редачим инлайн в текущей line
	if s.redact && deltaContent != "" {
		secrets := DetectSecrets(deltaContent)
		if len(secrets) > 0 {
			modified := string(line)
			for _, sec := range secrets {
				if len(sec.Value) >= 8 && strings.Contains(modified, sec.Value) {
					modified = strings.ReplaceAll(modified, sec.Value, "[REDACTED:"+sec.Type+"]")
					s.redacted = true
					s.secretTypes = append(s.secretTypes, sec.Type)
				}
			}
			return []byte(modified)
		}
	}

	return line
}

// Summary возвращает итоговый вердикт после завершения стрима.
func (s *StreamScanner) Summary() (injectionDetected, commandDetected, secretsRedacted bool, secretTypes []string) {
	return s.injected, s.commands, s.redacted, s.secretTypes
}
