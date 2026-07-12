package proxy

import (
	"testing"
)

func TestOpenAIToGemini_BasicConversion(t *testing.T) {
	temp := 0.7
	maxTok := int64(1024)
	req := &openAIChatRequest{
		Model: "gemini-2.0-flash",
		Messages: []openAIMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
			{Role: "user", Content: "How are you?"},
		},
		Temperature: &temp,
		MaxTokens:   &maxTok,
	}

	gem := openAIToGemini(req)

	if gem.SystemInstruction == nil {
		t.Fatal("expected systemInstruction")
	}
	if gem.SystemInstruction.Parts[0].Text != "You are helpful." {
		t.Errorf("system text = %q", gem.SystemInstruction.Parts[0].Text)
	}

	if len(gem.Contents) != 3 {
		t.Fatalf("expected 3 contents (2 user + 1 model), got %d", len(gem.Contents))
	}

	if gem.Contents[0].Role != "user" || gem.Contents[0].Parts[0].Text != "Hello" {
		t.Errorf("content[0] = %+v", gem.Contents[0])
	}
	if gem.Contents[1].Role != "model" || gem.Contents[1].Parts[0].Text != "Hi there" {
		t.Errorf("content[1] = %+v", gem.Contents[1])
	}
	if gem.Contents[2].Role != "user" || gem.Contents[2].Parts[0].Text != "How are you?" {
		t.Errorf("content[2] = %+v", gem.Contents[2])
	}

	if gem.GenerationConfig == nil {
		t.Fatal("expected generationConfig")
	}
	if gem.GenerationConfig.MaxOutputTokens == nil || *gem.GenerationConfig.MaxOutputTokens != 1024 {
		t.Errorf("maxOutputTokens = %v", gem.GenerationConfig.MaxOutputTokens)
	}
}

func TestOpenAIToGemini_NoSystem_NoConfig(t *testing.T) {
	req := &openAIChatRequest{
		Model: "gemini-2.0-flash",
		Messages: []openAIMessage{
			{Role: "user", Content: "Hi"},
		},
	}

	gem := openAIToGemini(req)

	if gem.SystemInstruction != nil {
		t.Error("expected nil systemInstruction")
	}
	if gem.GenerationConfig != nil {
		t.Error("expected nil generationConfig")
	}
	if len(gem.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(gem.Contents))
	}
}

func TestOpenAIToGemini_ArrayContent(t *testing.T) {
	req := &openAIChatRequest{
		Model: "gemini-2.0-flash",
		Messages: []openAIMessage{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "part1"},
				map[string]any{"type": "text", "text": "part2"},
			}},
		},
	}

	gem := openAIToGemini(req)
	if gem.Contents[0].Parts[0].Text != "part1part2" {
		t.Errorf("expected concatenated text, got %q", gem.Contents[0].Parts[0].Text)
	}
}

func TestGeminiToOpenAI_BasicResponse(t *testing.T) {
	gr := &geminiResponse{
		Candidates: []geminiCandidate{{
			Content: struct {
				Parts []geminiPart `json:"parts"`
				Role  string       `json:"role"`
			}{
				Parts: []geminiPart{{Text: "Hello from Gemini"}},
				Role:  "model",
			},
			FinishReason: "STOP",
		}},
		UsageMetadata: geminiUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 5,
			TotalTokenCount:      15,
		},
	}

	result := geminiToOpenAI(gr, "gemini-2.0-flash")

	choices, ok := result["choices"].([]map[string]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("expected 1 choice, got %v", result["choices"])
	}

	msg, ok := choices[0]["message"].(map[string]any)
	if !ok {
		t.Fatalf("expected message map, got %T", choices[0]["message"])
	}
	if msg["content"] != "Hello from Gemini" {
		t.Errorf("content = %v", msg["content"])
	}
	if choices[0]["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", choices[0]["finish_reason"])
	}

	usage, ok := result["usage"].(map[string]any)
	if !ok {
		t.Fatal("expected usage map")
	}
	if usage["total_tokens"].(int64) != 15 {
		t.Errorf("total_tokens = %v", usage["total_tokens"])
	}
}

func TestGeminiToOpenAI_MaxTokensFinish(t *testing.T) {
	gr := &geminiResponse{
		Candidates: []geminiCandidate{{
			Content: struct {
				Parts []geminiPart `json:"parts"`
				Role  string       `json:"role"`
			}{
				Parts: []geminiPart{{Text: "truncated"}},
				Role:  "model",
			},
			FinishReason: "MAX_TOKENS",
		}},
	}

	result := geminiToOpenAI(gr, "gemini-2.0-flash")
	choices := result["choices"].([]map[string]any)
	if choices[0]["finish_reason"] != "length" {
		t.Errorf("expected length, got %v", choices[0]["finish_reason"])
	}
}

func TestGeminiToOpenAI_EmptyCandidates(t *testing.T) {
	gr := &geminiResponse{}

	result := geminiToOpenAI(gr, "gemini-2.0-flash")
	choices := result["choices"].([]map[string]any)
	msg := choices[0]["message"].(map[string]any)
	if msg["content"] != "" {
		t.Errorf("expected empty content, got %v", msg["content"])
	}
}
