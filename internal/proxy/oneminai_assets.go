package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"szx-gateway/internal/keys"
)

const oneMinAIAssetTarget = "https://api.1min.ai/api/assets"

var oneMinUploadLimits = struct {
	sync.Mutex
	byKey map[string]*oneMinUploadLimit
}{byKey: make(map[string]*oneMinUploadLimit)}

type oneMinUploadLimit struct {
	sem      chan struct{}
	mu       sync.Mutex
	requests []time.Time
}

func (h *OneMinAIHandler) uploadAssets(r *http.Request, key *keys.KeyState, normalized oneMinNormalizedRequest) ([]string, []string, bool, error) {
	if len(normalized.Images)+len(normalized.Files) == 0 {
		return nil, nil, false, nil
	}
	limit := oneMinUploadLimitFor(key.KeyHash)
	var images, files []string
	for _, asset := range append(append([]oneMinAssetInput{}, normalized.Images...), normalized.Files...) {
		id, err := h.uploadAsset(r, key, limit, asset)
		if err != nil {
			return nil, nil, len(images)+len(files) > 0, err
		}
		if asset.Kind == "image" {
			images = append(images, id)
		} else {
			files = append(files, id)
		}
	}
	return images, files, len(images)+len(files) > 0, nil
}

func oneMinUploadLimitFor(hash string) *oneMinUploadLimit {
	oneMinUploadLimits.Lock()
	defer oneMinUploadLimits.Unlock()
	limit := oneMinUploadLimits.byKey[hash]
	if limit == nil {
		limit = &oneMinUploadLimit{sem: make(chan struct{}, 5)}
		oneMinUploadLimits.byKey[hash] = limit
	}
	return limit
}

func (h *OneMinAIHandler) uploadAsset(r *http.Request, key *keys.KeyState, limit *oneMinUploadLimit, asset oneMinAssetInput) (string, error) {
	mimeType, payload, err := oneMinDataURL(asset.DataURL, h.cfg.OneMinAIAssetMaxBytes)
	if err != nil {
		return "", fmt.Errorf("%s: %w", asset.Param, err)
	}
	if !oneMinAllowedMIME(asset.Kind, mimeType) {
		return "", fmt.Errorf("%s: unsupported %s MIME type %q", asset.Param, asset.Kind, mimeType)
	}
	select {
	case limit.sem <- struct{}{}:
		defer func() { <-limit.sem }()
	case <-r.Context().Done():
		return "", r.Context().Err()
	}
	if !limit.allow(time.Now()) {
		return "", fmt.Errorf("%s: 1min.AI asset upload rate limit reached", asset.Param)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filename := asset.Filename
	if filename == "" {
		filename = "image" + oneMinExtension(mimeType)
	}
	part, err := writer.CreateFormFile("asset", filename)
	if err != nil {
		return "", err
	}
	if _, err = part.Write(payload); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, oneMinAIAssetTarget, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("API-KEY", key.RawKey)
	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("asset upload transport failure")
	} // non-idempotent: caller must not retry
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("asset upload returned %d", resp.StatusCode)
	}
	var result struct {
		Asset struct {
			Key string `json:"key"`
		} `json:"asset"`
		FileContent struct {
			Path string `json:"path"`
			UUID string `json:"uuid"`
		} `json:"fileContent"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid asset upload response")
	}
	if asset.Kind == "image" {
		if result.Asset.Key != "" {
			return result.Asset.Key, nil
		}
		if result.FileContent.Path != "" {
			return result.FileContent.Path, nil
		}
	} else if result.FileContent.UUID != "" {
		return result.FileContent.UUID, nil
	}
	return "", fmt.Errorf("invalid asset upload response")
}

func (l *oneMinUploadLimit) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	keep := l.requests[:0]
	for _, t := range l.requests {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	l.requests = keep
	if len(l.requests) >= 100 {
		return false
	}
	l.requests = append(l.requests, now)
	return true
}

func oneMinDataURL(value string, max int64) (string, []byte, error) {
	head, encoded, ok := strings.Cut(value, ",")
	if !ok || !strings.HasPrefix(head, "data:") || !strings.HasSuffix(head, ";base64") {
		return "", nil, fmt.Errorf("only base64 data URLs are supported; external URLs are not supported")
	}
	mimeType := strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64")
	if mimeType == "" {
		return "", nil, fmt.Errorf("data URL MIME type is required")
	}
	if int64(base64.StdEncoding.DecodedLen(len(encoded))) > max {
		return "", nil, fmt.Errorf("decoded file exceeds configured size limit")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("invalid base64 data URL")
	}
	if int64(len(decoded)) > max {
		return "", nil, fmt.Errorf("decoded file exceeds configured size limit")
	}
	return mimeType, decoded, nil
}
func oneMinAllowedMIME(kind, value string) bool {
	images := map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true, "image/svg+xml": true}
	files := map[string]bool{"application/pdf": true, "application/msword": true, "application/vnd.openxmlformats-officedocument.wordprocessingml.document": true, "text/plain": true, "application/json": true, "text/csv": true, "application/xml": true, "text/xml": true}
	if kind == "image" {
		return images[value]
	}
	return files[value]
}
func oneMinExtension(mimeType string) string {
	m := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif", "image/svg+xml": ".svg"}
	return m[mimeType]
}
func oneMinAssetSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
