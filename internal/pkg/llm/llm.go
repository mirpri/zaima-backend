// Package llm 提供一个与 OpenAI 兼容的大模型客户端 (DeepSeek / 通义 / OpenAI 等)。
//
// 设计原则:
//   - 未配置 API Key 时 Chat 返回错误，调用方回退到规则引擎，保证功能不中断。
//   - 内置超时，避免慢响应拖垮请求链路。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"zaima-backend/internal/config"
)

// ErrNotConfigured 表示未配置 LLM，调用方应回退到兜底逻辑。
var ErrNotConfigured = errors.New("llm 未配置 api_key")

// Message 一条对话消息。
type Message struct {
	Role    string `json:"role"` // system / user / assistant
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Enabled 报告当前是否已配置可用的 LLM。
func Enabled() bool {
	return config.AppConfig != nil && config.AppConfig.LLM.APIKey != ""
}

// Chat 发起一次对话补全，返回助手文本。
func Chat(ctx context.Context, messages []Message) (string, error) {
	if !Enabled() {
		return "", ErrNotConfigured
	}
	cfg := config.AppConfig.LLM

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	model := cfg.Model
	if model == "" {
		model = "deepseek-chat"
	}
	reqBody, _ := json.Marshal(chatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: 0.7,
		MaxTokens:   512,
	})

	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("llm 返回错误: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("llm 无返回内容")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}
