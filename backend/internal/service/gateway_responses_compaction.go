package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const defaultGatewayCompactFallbackSecret = "sub2api-gateway-compact-default-secret"

func gatewayCompactCipherSecret(cfg *config.Config) string {
	if cfg != nil {
		if secret := strings.TrimSpace(cfg.JWT.Secret); secret != "" {
			return secret
		}
	}
	return defaultGatewayCompactFallbackSecret
}

func gatewayCompactUserAAD(ctx context.Context) []byte {
	userID := int64(0)
	if ctx != nil {
		if id, ok := ctx.Value(ctxkey.UserID).(int64); ok && id > 0 {
			userID = id
		}
	}
	return []byte(deepSeekCompactCipherDomain + ":user:" + strconv.FormatInt(userID, 10))
}

func gatewayCompactAEAD(cfg *config.Config) (cipher.AEAD, error) {
	secret := gatewayCompactCipherSecret(cfg)
	key := sha256.Sum256([]byte(deepSeekCompactCipherDomain + "\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("initialize gateway compact cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize gateway compact AEAD: %w", err)
	}
	return aead, nil
}

func sealGatewayCompactCheckpoint(ctx context.Context, cfg *config.Config, checkpoint string) (string, error) {
	if !utf8.ValidString(checkpoint) || len(checkpoint) > deepSeekCompactMaxSummaryBytes {
		return "", errors.New("gateway compact checkpoint exceeds the supported size")
	}
	payload, err := marshalOpenAIUpstreamJSON(deepSeekCompactEnvelope{
		Version:    deepSeekCompactEnvelopeVersion,
		Checkpoint: checkpoint,
	})
	if err != nil {
		return "", fmt.Errorf("encode gateway compact checkpoint: %w", err)
	}
	aead, err := gatewayCompactAEAD(cfg)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate gateway compact nonce: %w", err)
	}
	aad := gatewayCompactUserAAD(ctx)
	sealed := aead.Seal(nonce, nonce, payload, aad)
	envelope := deepSeekCompactEnvelopePrefix + base64.RawURLEncoding.EncodeToString(sealed)
	if len(envelope) > deepSeekCompactMaxEnvelopeBytes {
		return "", errors.New("gateway compact encrypted_content exceeds the supported size")
	}
	return envelope, nil
}

func openGatewayCompactCheckpoint(ctx context.Context, cfg *config.Config, envelope string) (string, error) {
	invalid := func() (string, error) { return "", ErrDeepSeekCompactInvalidEncryptedContent }
	if !strings.HasPrefix(envelope, deepSeekCompactEnvelopePrefix) || len(envelope) > deepSeekCompactMaxEnvelopeBytes {
		return invalid()
	}
	encoded := strings.TrimPrefix(envelope, deepSeekCompactEnvelopePrefix)
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return invalid()
	}
	aead, err := gatewayCompactAEAD(cfg)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return invalid()
	}
	aad := gatewayCompactUserAAD(ctx)
	payload, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if err != nil || len(payload) > deepSeekCompactMaxEnvelopeBytes || !utf8.Valid(payload) {
		return invalid()
	}
	var state deepSeekCompactEnvelope
	if err := json.Unmarshal(payload, &state); err != nil ||
		state.Version != deepSeekCompactEnvelopeVersion ||
		strings.TrimSpace(state.Checkpoint) == "" ||
		len(state.Checkpoint) > deepSeekCompactMaxSummaryBytes ||
		!utf8.ValidString(state.Checkpoint) {
		return invalid()
	}
	return state.Checkpoint, nil
}

// RestoreGatewayCompactInput turns gateway-owned compaction items back into
// provider-neutral user checkpoints before policy inspection or upstream conversion.
func (s *GatewayService) RestoreGatewayCompactInput(ctx context.Context, body []byte) ([]byte, bool, error) {
	var cfg *config.Config
	if s != nil {
		cfg = s.cfg
	}
	return restoreGatewayCompactInput(ctx, cfg, body)
}

func restoreGatewayCompactInput(ctx context.Context, cfg *config.Config, body []byte) ([]byte, bool, error) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, false, nil
	}
	inputResult := gjson.GetBytes(body, "input")
	if !inputResult.IsArray() {
		return body, false, nil
	}
	hasOwnedCompaction := false
	inputResult.ForEach(func(_, item gjson.Result) bool {
		t := item.Get("type").String()
		if (t == "compaction" || t == "compaction_summary") &&
			strings.HasPrefix(item.Get("encrypted_content").String(), deepSeekCompactEnvelopePrefix) {
			hasOwnedCompaction = true
			return false
		}
		return true
	})
	if !hasOwnedCompaction {
		return body, false, nil
	}

	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return body, false, nil
	}
	inputRaw, ok := request["input"]
	if !ok || len(bytes.TrimSpace(inputRaw)) == 0 || bytes.TrimSpace(inputRaw)[0] != '[' {
		return body, false, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(inputRaw, &items); err != nil {
		return body, false, nil
	}

	changed := false
	for i, raw := range items {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		var itemType string
		if err := json.Unmarshal(item["type"], &itemType); err != nil ||
			(itemType != "compaction" && itemType != "compaction_summary") {
			continue
		}
		var encryptedContent string
		if err := json.Unmarshal(item["encrypted_content"], &encryptedContent); err != nil {
			continue
		}
		if !strings.HasPrefix(encryptedContent, deepSeekCompactEnvelopePrefix) {
			continue
		}
		checkpoint, err := openGatewayCompactCheckpoint(ctx, cfg, encryptedContent)
		if err != nil {
			return nil, false, err
		}
		restored, err := marshalOpenAIUpstreamJSON(map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": checkpoint,
			}},
		})
		if err != nil {
			return nil, false, err
		}
		items[i] = restored
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	restoredInput, err := marshalOpenAIUpstreamJSON(items)
	if err != nil {
		return nil, false, err
	}
	request["input"] = restoredInput
	restoredBody, err := marshalOpenAIUpstreamJSON(request)
	if err != nil {
		return nil, false, err
	}
	return restoredBody, true, nil
}

func isGatewayResponsesCompactionRequest(c *gin.Context, body []byte) bool {
	return isOpenAIResponsesCompactPath(c) || HasCompactionTriggerInInput(body)
}

func rewriteGatewayResponsesCompactionRequest(body []byte) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("parse compact request: %w", err)
	}
	inputRaw, ok := request["input"]
	if !ok {
		return nil, errors.New("remote compaction requires input")
	}

	var items []json.RawMessage
	trimmedInput := bytes.TrimSpace(inputRaw)
	if len(trimmedInput) > 0 && trimmedInput[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmedInput, &text); err != nil || strings.TrimSpace(text) == "" {
			return nil, errors.New("remote compaction input string must not be empty")
		}
		msg, err := marshalOpenAIUpstreamJSON(map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": text,
			}},
		})
		if err != nil {
			return nil, err
		}
		items = []json.RawMessage{msg}
	} else if err := json.Unmarshal(inputRaw, &items); err != nil {
		return nil, errors.New("remote compaction input must be a string or array")
	}

	history := make([]json.RawMessage, 0, len(items))
	for _, raw := range items {
		if strings.TrimSpace(gjson.GetBytes(raw, "type").String()) == "compaction_trigger" {
			continue
		}
		history = append(history, raw)
	}
	if len(history) == 0 {
		return nil, errors.New("remote compaction has no conversation history to summarize")
	}

	promptItem, err := marshalOpenAIUpstreamJSON(map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{map[string]any{
			"type": "input_text",
			"text": deepSeekCompactInstruction,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode compact checkpoint prompt: %w", err)
	}
	upstreamInput, err := marshalOpenAIUpstreamJSON(append(history, promptItem))
	if err != nil {
		return nil, fmt.Errorf("encode compact input: %w", err)
	}

	request["input"] = upstreamInput
	delete(request, "tools")
	delete(request, "tool_choice")

	return marshalOpenAIUpstreamJSON(request)
}

func extractAnthropicResponseSummaryText(resp *apicompat.AnthropicResponse) string {
	if resp == nil {
		return ""
	}
	var sb strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(block.Text)
		}
	}
	return strings.TrimSpace(sb.String())
}

func writeGatewayCompactResponse(
	ctx context.Context,
	c *gin.Context,
	cfg *config.Config,
	originalModel string,
	summary string,
	usage ClaudeUsage,
	clientStream bool,
) error {
	checkpoint := frameDeepSeekCompactSummary(summary)
	encryptedContent, err := sealGatewayCompactCheckpoint(ctx, cfg, checkpoint)
	if err != nil {
		return err
	}
	openAIUsage := OpenAIUsage{
		InputTokens:          usage.InputTokens,
		OutputTokens:         usage.OutputTokens,
		CacheReadInputTokens: usage.CacheReadInputTokens,
	}
	totalTokens := usage.InputTokens + usage.OutputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	responseID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	itemID := "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	finalJSON, err := deepSeekCompactResponsesJSON(responseID, itemID, originalModel, encryptedContent, openAIUsage, totalTokens)
	if err != nil {
		return err
	}

	MarkResponseCommitted(c)
	if clientStream {
		payload, ok := buildOpenAICompactSSEPayload(finalJSON)
		if !ok {
			return errors.New("failed to build compact SSE payload")
		}
		header := c.Writer.Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		header.Set("Connection", "keep-alive")
		header.Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write(payload)
		c.Writer.Flush()
		return nil
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Data(http.StatusOK, "application/json; charset=utf-8", finalJSON)
	return nil
}
