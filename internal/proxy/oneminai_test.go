package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClassifyOneMinAIError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   oneMinAIErrorAction
	}{
		{"unauthorized invalidates key", http.StatusUnauthorized, oneMinAIInvalidateKey},
		{"forbidden is temporary", http.StatusForbidden, oneMinAIRetryTemporary},
		{"rate limited gets dedicated cooldown", http.StatusTooManyRequests, oneMinAIRetryRateLimited},
		{"invalid request is temporary", http.StatusBadRequest, oneMinAIRetryTemporary},
		{"validation error is temporary", http.StatusUnprocessableEntity, oneMinAIRetryTemporary},
		{"server error is temporary", http.StatusBadGateway, oneMinAIRetryTemporary},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyOneMinAIError(tt.status); got != tt.want {
				t.Fatalf("classifyOneMinAIError(%d) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestOneMinPrompt(t *testing.T) {
	tests := []struct {
		name    string
		request oneMinOpenAIRequest
		want    string
		wantErr string
	}{
		{
			name: "preserves string history",
			request: oneMinOpenAIRequest{Messages: []oneMinAIMessage{
				{Role: "system", Content: "Be concise."},
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi"},
			}},
			want: "SYSTEM: Be concise.\n\nUSER: Hello\n\nASSISTANT: Hi",
		},
		{
			name: "accepts ordered text parts",
			request: oneMinOpenAIRequest{Messages: []oneMinAIMessage{{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "Review:\n```go\n"},
				map[string]any{"type": "text", "text": "fmt.Println(1)\n```"},
			}}}},
			want: "USER: Review:\n```go\nfmt.Println(1)\n```",
		},
		{
			name: "rejects image without dropping text",
			request: oneMinOpenAIRequest{Messages: []oneMinAIMessage{{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "Describe this"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/a.png"}},
			}}}},
			wantErr: `messages[0].content[1]: unsupported content type "image_url"`,
		},
		{
			name:    "rejects nil content",
			request: oneMinOpenAIRequest{Messages: []oneMinAIMessage{{Role: "user", Content: nil}}},
			wantErr: "messages[0].content: only text string or text content parts",
		},
		{
			name: "rejects malformed text part",
			request: oneMinOpenAIRequest{Messages: []oneMinAIMessage{{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": 1},
			}}}},
			wantErr: "messages[0].content[0]: text must be a string",
		},
		{
			name:    "rejects unsupported role",
			request: oneMinOpenAIRequest{Messages: []oneMinAIMessage{{Role: "tool", Content: "result"}}},
			wantErr: `messages[0]: unsupported role "tool"`,
		},
		{
			name: "rejects assistant tool call",
			request: oneMinOpenAIRequest{Messages: []oneMinAIMessage{{
				Role: "assistant", Content: nil, ToolCalls: json.RawMessage(`[{"id":"call_1"}]`),
			}}},
			wantErr: "tools: definitions are required when continuing tool-call history",
		},
		{
			name:    "rejects request tool definitions",
			request: oneMinOpenAIRequest{Messages: []oneMinAIMessage{{Role: "user", Content: "Hi"}}, Tools: json.RawMessage(`[{"type":"function"}]`)},
			wantErr: "tools[0]: must be a valid function definition",
		},
		{
			name:    "allows explicit no tools",
			request: oneMinOpenAIRequest{Messages: []oneMinAIMessage{{Role: "user", Content: "Hi"}}, Tools: json.RawMessage(`[]`), ToolChoice: json.RawMessage(`"none"`), ParallelToolCalls: json.RawMessage(`false`)},
			want:    "USER: Hi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := oneMinPrompt(tt.request)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("oneMinPrompt() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("oneMinPrompt() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("oneMinPrompt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOneMinTextContentRejectsArbitraryJSON(t *testing.T) {
	values := []any{nil, true, float64(1), map[string]any{}, []any{nil}, []any{map[string]any{"type": 1, "text": "x"}}}
	for _, value := range values {
		if _, err := oneMinTextContent(value, 0); err == nil {
			t.Errorf("oneMinTextContent(%#v) succeeded", value)
		}
	}
}

func TestWriteOneMinStreamChunk(t *testing.T) {
	res := httptest.NewRecorder()
	writeOneMinStreamChunk(res, res, "model", map[string]any{"role": "assistant"}, nil)
	finish := "stop"
	writeOneMinStreamChunk(res, res, "model", map[string]any{}, &finish)
	body := res.Body.String()
	if !strings.Contains(body, `"role":"assistant"`) || !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("unexpected chunks: %s", body)
	}
}

func TestNormalizeOneMinRequestAttachmentsAndOptions(t *testing.T) {
	memory := true
	_ = memory
	in := oneMinOpenAIRequest{Messages: []oneMinAIMessage{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "Read "},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,eA=="}},
			map[string]any{"type": "text", "text": " and file"},
			map[string]any{"type": "file", "file": map[string]any{"filename": "a.pdf", "file_data": "data:application/pdf;base64,eA=="}},
		}},
	}, OneMinAI: json.RawMessage(`{"brandVoiceId":"voice","memory":true,"webSearch":{"enabled":true,"numOfSite":3,"maxWord":1000}}`), Metadata: json.RawMessage(`{"requestSource":"test"}`)}
	out, err := normalizeOneMinRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Prompt != "SYSTEM: Be concise.\n\nUSER: Read  and file" || len(out.Images) != 1 || len(out.Files) != 1 || out.Memory == nil || !*out.Memory || out.WebSearch["numOfSite"] != 3 {
		t.Fatalf("unexpected normalized request: %#v", out)
	}
}

func TestNormalizeOneMinRequestRejectsHistoricalAttachment(t *testing.T) {
	_, err := normalizeOneMinRequest(oneMinOpenAIRequest{Messages: []oneMinAIMessage{
		{Role: "user", Content: []any{map[string]any{"type": "text", "text": "old"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,eA=="}}}},
		{Role: "user", Content: "new"},
	}})
	if err == nil || !strings.Contains(err.Error(), "only in the last user") {
		t.Fatalf("expected historical attachment error, got %v", err)
	}
}

func TestOneMinDataURL(t *testing.T) {
	mime, payload, err := oneMinDataURL("data:application/pdf;base64,aGVsbG8=", 10)
	if err != nil || mime != "application/pdf" || string(payload) != "hello" || oneMinAssetSHA256(payload) == "" {
		t.Fatalf("unexpected data URL result %q %q %v", mime, payload, err)
	}
	if _, _, err := oneMinDataURL("https://example.test/file.pdf", 10); err == nil {
		t.Fatal("external URL accepted")
	}
}

func TestNormalizeOneMinRequestEmulatedTools(t *testing.T) {
	in := oneMinOpenAIRequest{
		Messages: []oneMinAIMessage{
			{Role: "user", Content: "Weather?"},
			{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Moscow\"}"}}]`)},
			{Role: "tool", ToolCallID: "call_1", Content: `{"temp":20}`},
		},
		Tools: json.RawMessage(`[{"type":"function","function":{"name":"get_weather","description":"Gets weather","parameters":{"type":"object"}}}]`),
	}
	out, err := normalizeOneMinRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if !out.EmulatedTools || len(out.Tools) != 1 || !strings.Contains(out.Prompt, "ASSISTANT TOOL CALL call_1 get_weather") || !strings.Contains(out.Prompt, `TOOL RESULT call_1: {"temp":20}`) {
		t.Fatalf("unexpected normalized request: %#v", out)
	}
}

func TestNormalizeOneMinRequestRejectsUnknownToolResult(t *testing.T) {
	_, err := normalizeOneMinRequest(oneMinOpenAIRequest{
		Messages: []oneMinAIMessage{{Role: "tool", ToolCallID: "call_missing", Content: "result"}},
		Tools:    json.RawMessage(`[{"type":"function","function":{"name":"lookup","parameters":{}}}]`),
	})
	if err == nil || !strings.Contains(err.Error(), "does not match an earlier") {
		t.Fatalf("expected unknown tool result error, got %v", err)
	}
}

func TestNormalizeOneMinRequestEmulatesToolsAutomatically(t *testing.T) {
	out, err := normalizeOneMinRequest(oneMinOpenAIRequest{
		Messages: []oneMinAIMessage{{Role: "user", Content: "Weather?"}},
		Tools:    json.RawMessage(`[{"type":"function","function":{"name":"weather","parameters":{}}}]`),
	})
	if err != nil || !out.EmulatedTools {
		t.Fatalf("emulated tools were not enabled: %#v, err=%v", out, err)
	}
}

func TestNormalizeOneMinRequestAcceptsToolsWithoutParameters(t *testing.T) {
	out, err := normalizeOneMinRequest(oneMinOpenAIRequest{
		Messages: []oneMinAIMessage{{Role: "user", Content: "Run status"}},
		Tools:    json.RawMessage(`[{"type":"function","function":{"name":"status"}},{"type":"function","function":{"name":"health","input_schema":{"type":"object"}}}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Tools[0].Function.Parameters) != `{}` || string(out.Tools[1].Function.Parameters) != `{"type":"object"}` {
		t.Fatalf("unexpected normalized parameters: %#v", out.Tools)
	}
}

func TestNormalizeOneMinRequestAcceptsLongToolDescription(t *testing.T) {
	description, err := json.Marshal(strings.Repeat("x", 5000))
	if err != nil {
		t.Fatal(err)
	}
	tools := json.RawMessage(`[{"type":"function","function":{"name":"long_description","description":` + string(description) + `,"parameters":{}}}]`)
	if _, err := normalizeOneMinRequest(oneMinOpenAIRequest{Messages: []oneMinAIMessage{{Role: "user", Content: "Use tool"}}, Tools: tools}); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeOneMinRequestAllowsParallelToolCalls(t *testing.T) {
	_, err := normalizeOneMinRequest(oneMinOpenAIRequest{
		Messages:          []oneMinAIMessage{{Role: "user", Content: "Weather?"}},
		Tools:             json.RawMessage(`[{"type":"function","function":{"name":"weather","parameters":{}}}]`),
		ParallelToolCalls: json.RawMessage(`true`),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOneMinEmulatedToolCalls(t *testing.T) {
	tools := []oneMinToolDefinition{{Type: "function"}}
	tools[0].Function.Name = "get_weather"
	calls, ok := oneMinEmulatedToolCalls(`{"tool_calls":[{"name":"get_weather","arguments":{"city":"Moscow"}}]}`, tools)
	if !ok || len(calls) != 1 || calls[0]["id"] != "call_1min_1" {
		t.Fatalf("unexpected tool calls: %#v, ok=%v", calls, ok)
	}
	if _, ok := oneMinEmulatedToolCalls(`{"tool_calls":[{"name":"unknown","arguments":{}}]}`, tools); ok {
		t.Fatal("undeclared tool accepted")
	}
}

func TestOneMinEmulatedContent(t *testing.T) {
	content, ok := oneMinEmulatedContent(`{"content":"Final answer"}`)
	if !ok || content != "Final answer" {
		t.Fatalf("unexpected content %q, ok=%v", content, ok)
	}
}

func TestOneMinEmulatedStream(t *testing.T) {
	res := httptest.NewRecorder()
	h := &OneMinAIHandler{}
	h.emulatedStream(res, "model", map[string]any{
		"role":    "assistant",
		"content": nil,
		"tool_calls": []map[string]any{{
			"id":   "call_1min_1",
			"type": "function",
			"function": map[string]string{
				"name":      "glob",
				"arguments": `{"pattern":"**/*"}`,
			},
		}},
	}, "tool_calls")
	body := res.Body.String()
	if !strings.Contains(body, `"tool_calls"`) || !strings.Contains(body, `"finish_reason":"tool_calls"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("unexpected stream: %s", body)
	}
}

func TestOneMinEmulatedToolsPromptCompactsOpenCodeSchema(t *testing.T) {
	tools := []oneMinToolDefinition{{Type: "function"}}
	tools[0].Function.Name = "bash"
	tools[0].Function.Description = strings.Repeat("system instructions ", 1000)
	tools[0].Function.Parameters = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"command":{"type":"string","description":"command"},"timeout":{"type":"integer","minimum":1}},"required":["command"]}`)
	prompt := oneMinEmulatedToolsPrompt(tools, "auto")
	if len(prompt) > 512 || strings.Contains(prompt, "system instructions") || strings.Contains(prompt, "$schema") || strings.Contains(prompt, "description") {
		t.Fatalf("unsafe tool prompt: %s", prompt)
	}
	if !strings.Contains(prompt, `"command":"string"`) || !strings.Contains(prompt, `"timeout":"integer"`) {
		t.Fatalf("tool parameters missing: %s", prompt)
	}
}
