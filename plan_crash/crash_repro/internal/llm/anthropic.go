package llm

// Ported from TV3's hand-over (C_TV3_llm_logprofile, http.go and fake.go):
// the native Claude backend, token counting for the cost column of the
// evaluation, and an offline provider that exercises the whole LLM path.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrRefused is returned when the model declined to answer.
var ErrRefused = errors.New("llm: the model declined the request")

// Usage counts requests and tokens (plan, week 4: cost of the LLM layer).
type Usage struct {
	Requests     int `json:"requests"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	Truncated    int `json:"truncated"` // answers cut at the token limit
}

// Sub returns u minus earlier totals b.
func (u Usage) Sub(b Usage) Usage {
	return Usage{u.Requests - b.Requests, u.InputTokens - b.InputTokens, u.OutputTokens - b.OutputTokens, u.Truncated - b.Truncated}
}

// Cost returns the price in USD for the given prices per million tokens.
func (u Usage) Cost(inPerM, outPerM float64) float64 {
	return (float64(u.InputTokens)*inPerM + float64(u.OutputTokens)*outPerM) / 1e6
}

// Metered is implemented by providers that count tokens.
type Metered interface {
	Usage() Usage
}

// UsageOf returns p's totals, or zero if p does not count.
func UsageOf(p Provider) Usage {
	if m, ok := p.(Metered); ok {
		return m.Usage()
	}
	return Usage{}
}

// meter is embedded by the HTTP providers.
type meter struct {
	mu sync.Mutex
	u  Usage
}

func (m *meter) add(in, out int, truncated bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.u.Requests++
	m.u.InputTokens += in
	m.u.OutputTokens += out
	if truncated {
		m.u.Truncated++
	}
}

// Usage implements Metered.
func (m *meter) Usage() Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.u
}

// Default endpoint and model of the Anthropic backend.
const (
	DefaultAnthropicURL   = "https://api.anthropic.com/v1"
	DefaultAnthropicModel = "claude-opus-5-5"
)

// AnthropicProvider talks to the Claude Messages API (POST /v1/messages)
// over plain HTTP, so gofl keeps zero third-party dependencies and builds
// offline. Every call is a fresh one-message conversation (C11).
//
// No temperature is sent: current Claude models reject sampling parameters
// (HTTP 400); their default sampling stands in for paper C's 1.0.
type AnthropicProvider struct {
	BaseURL   string
	Model     string
	Key       string // sent as x-api-key, never logged
	MaxTokens int    // 0: 16000
	// Effort is output_config.effort ("low" ... "max"); "" keeps the
	// model's default. Fixed for a whole measurement run.
	Effort string
	// Fallback "default" enables the server-side refusal fallback (beta).
	// Off by default: an answer from another model would mix two models in
	// one measurement.
	Fallback   string
	Client     *http.Client
	MaxRetries int
	Backoff    time.Duration
	meter
}

// NewAnthropicProvider returns a provider with safe defaults.
func NewAnthropicProvider(baseURL, model, key string) *AnthropicProvider {
	return &AnthropicProvider{BaseURL: baseURL, Model: model, Key: key, MaxTokens: 16000,
		Client: &http.Client{Timeout: 5 * time.Minute}, MaxRetries: 3, Backoff: time.Second}
}

// Name identifies the backend in reports.
func (p *AnthropicProvider) Name() string { return "anthropic:" + p.Model }

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements Provider.
func (p *AnthropicProvider) Complete(ctx context.Context, prompt string) (string, error) {
	maxTok := p.MaxTokens
	if maxTok <= 0 {
		maxTok = 16000
	}
	body := map[string]any{
		"model":      p.Model,
		"max_tokens": maxTok,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	}
	if p.Effort != "" {
		body["output_config"] = map[string]any{"effort": p.Effort}
	}
	hdr := map[string]string{"x-api-key": p.Key, "anthropic-version": "2023-06-01"}
	if p.Fallback != "" {
		body["fallbacks"] = p.Fallback
		hdr["anthropic-beta"] = "server-side-fallback-2026-07-01"
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	var out anthropicResponse
	if err := retryPost(ctx, p.Client, p.MaxRetries, p.Backoff, strings.TrimRight(p.BaseURL, "/")+"/messages", hdr, payload, &out); err != nil {
		return "", err
	}
	p.add(out.Usage.InputTokens, out.Usage.OutputTokens, out.StopReason == "max_tokens")
	if out.StopReason == "refusal" {
		return "", ErrRefused
	}
	var sb strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	// A reply cut at max_tokens is returned as is: ParseResponse repairs
	// truncated JSON (C13).
	return sb.String(), nil
}

// retryPost POSTs payload and decodes a 200 reply into out, retrying
// 429/5xx and network errors with doubling waits. Errors never include the
// request (its headers carry the key).
func retryPost(ctx context.Context, c *http.Client, retries int, wait time.Duration, endpoint string, hdr map[string]string, payload []byte, out any) error {
	if c == nil {
		c = http.DefaultClient
	}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := c.Do(req)
		if err != nil {
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = fmt.Errorf("llm: request failed: %w", err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("llm: reading response: %w", err)
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("llm: server answered %s", resp.Status)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			msg := ""
			var e struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(data, &e) == nil && e.Error != nil {
				msg = ": " + e.Error.Message
			}
			return fmt.Errorf("llm: server answered %s%s", resp.Status, msg)
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("llm: bad response body: %w", err)
		}
		return nil
	}
	return lastErr
}

// FirstFrameProvider answers like a weak model, offline: it names the first
// user frame of the stack in the prompt, in the messy shapes real models
// produce (plain JSON, prose around a fenced block, a truncated reply), so
// the whole LLM path, parser and vote included, runs without a network
// ("-llm fake"). Its accuracy equals the no-LLM baseline by construction.
type FirstFrameProvider struct {
	mu    sync.Mutex
	calls int
}

// Name identifies the backend in reports.
func (f *FirstFrameProvider) Name() string { return "fake:first-frame" }

// Complete implements Provider.
func (f *FirstFrameProvider) Complete(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	n := f.calls
	f.calls++
	f.mu.Unlock()
	var file, fn, line string
	for _, fr := range UserFrames(prompt) {
		if strings.HasPrefix(fr.Func, "net/http.") || strings.HasPrefix(fr.File, "<goroot>") || strings.Contains(fr.File, "/pkg/mod/") {
			continue
		}
		fn, file, line = fr.Func, strings.TrimPrefix(fr.File, "<root>/"), fmt.Sprint(fr.Line)
		break
	}
	if file == "" {
		return "I could not find the crash location.", nil
	}
	js := fmt.Sprintf(`{"locations":[{"file":%q,"function":%q,"line":%s}],"explanation":"top user frame"}`, file, fn, line)
	switch n % 3 {
	case 0:
		return js, nil
	case 1:
		return "The bug is most likely here.\n```json\n" + js + "\n```\nHope this helps!", nil
	default:
		return js[:len(js)*2/3], nil
	}
}

// Calls is the number of Complete calls so far.
func (f *FirstFrameProvider) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Named is implemented by providers that can say which backend they are.
type Named interface {
	Name() string
}

// NameOf returns p's name for reports, or its Go type.
func NameOf(p Provider) string {
	if n, ok := p.(Named); ok {
		return n.Name()
	}
	return fmt.Sprintf("%T", p)
}
