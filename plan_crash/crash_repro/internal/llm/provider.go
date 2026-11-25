package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Environment variables read by NewProvider. Keys are never taken from flags
// or files of the repository.
const (
	EnvURL         = "GOFL_LLM_URL"         // base URL, e.g. http://localhost:11434/v1
	EnvModel       = "GOFL_LLM_MODEL"       // model name
	EnvKey         = "GOFL_LLM_KEY"         // API key (optional for local)
	EnvTemperature = "GOFL_LLM_TEMPERATURE" // default 1.0, as in paper C (not sent to Anthropic)
	EnvProvider    = "GOFL_LLM_PROVIDER"    // api mode: openai (default, any OpenAI-compatible server) or anthropic
	EnvTimeout     = "GOFL_LLM_TIMEOUT"     // per request, Go duration, default 5m
	EnvEffort      = "GOFL_LLM_EFFORT"      // anthropic only: output_config.effort
	EnvFallback    = "GOFL_LLM_FALLBACK"    // anthropic only: "default" enables the refusal fallback

	// Fallback key variables, read only when GOFL_LLM_KEY is empty.
	EnvAnthropicKey = "ANTHROPIC_API_KEY"
	EnvOpenAIKey    = "OPENAI_API_KEY"
)

// DefaultLocalURL is the default OpenAI-compatible endpoint of Ollama.
const DefaultLocalURL = "http://localhost:11434/v1"

// ErrOff is returned by NewProvider for mode "off".
var ErrOff = errors.New("llm: mode is off")

// HTTPProvider talks to an OpenAI-compatible chat-completions endpoint
// (OpenAI, Ollama, llama.cpp server, LM Studio, ...). Every Complete call is a
// fresh single-message conversation, as paper C requires for independent
// attempts.
type HTTPProvider struct {
	BaseURL     string
	Model       string
	Key         string // sent as "Authorization: Bearer", never logged
	Temperature float64
	Client      *http.Client
	MaxRetries  int           // retries after the first try for 429/5xx/network errors
	Backoff     time.Duration // first wait, doubled each retry
	meter
}

// NewHTTPProvider returns a provider with safe defaults.
func NewHTTPProvider(baseURL, model, key string) *HTTPProvider {
	return &HTTPProvider{
		BaseURL:     baseURL,
		Model:       model,
		Key:         key,
		Temperature: 1.0,
		Client:      &http.Client{Timeout: 5 * time.Minute},
		MaxRetries:  3,
		Backoff:     time.Second,
	}
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Name identifies the backend in reports.
func (p *HTTPProvider) Name() string { return "openai-compatible:" + p.Model }

func (p *HTTPProvider) endpoint() string {
	u := strings.TrimRight(p.BaseURL, "/")
	if strings.HasSuffix(u, "/chat/completions") {
		return u
	}
	return u + "/chat/completions"
}

// Complete implements Provider.
func (p *HTTPProvider) Complete(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:       p.Model,
		Messages:    []chatMessage{{Role: "user", Content: prompt}},
		Temperature: p.Temperature,
	})
	if err != nil {
		return "", err
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	wait := p.Backoff
	var lastErr error
	for attempt := 0; attempt <= p.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}
		out, retry, err := p.once(ctx, client, body)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return "", lastErr
}

// once does one HTTP round trip. retry says whether another try can help.
func (p *HTTPProvider) once(ctx context.Context, client *http.Client, body []byte) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(), bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.Key != "" {
		req.Header.Set("Authorization", "Bearer "+p.Key)
	}
	resp, err := client.Do(req)
	if err != nil {
		// Do not include the request (it carries the key header) in the error.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", true, fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", true, fmt.Errorf("llm: reading response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", true, fmt.Errorf("llm: server answered %s", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		var cr chatResponse
		msg := ""
		if json.Unmarshal(data, &cr) == nil && cr.Error != nil {
			msg = ": " + cr.Error.Message
		}
		return "", false, fmt.Errorf("llm: server answered %s%s", resp.Status, msg)
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", false, fmt.Errorf("llm: bad response body: %w", err)
	}
	if cr.Error != nil {
		return "", false, fmt.Errorf("llm: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", false, errors.New("llm: response has no choices")
	}
	p.add(cr.Usage.PromptTokens, cr.Usage.CompletionTokens, cr.Choices[0].FinishReason == "length")
	return cr.Choices[0].Message.Content, false, nil
}

// NewProvider builds the provider for -llm local|api from the environment.
func NewProvider(mode string) (Provider, error) {
	return NewProviderFrom(mode, os.Getenv)
}

// NewProviderFrom is NewProvider with an injectable environment (for tests).
//
//	local: GOFL_LLM_MODEL required; URL defaults to Ollama and MUST be a
//	       loopback address (nothing may leave the machine in this mode).
//	api:   GOFL_LLM_PROVIDER=openai (default): GOFL_LLM_URL, GOFL_LLM_MODEL
//	       and GOFL_LLM_KEY (or OPENAI_API_KEY) all required.
//	       GOFL_LLM_PROVIDER=anthropic: key from GOFL_LLM_KEY or
//	       ANTHROPIC_API_KEY; URL and model have defaults.
//	fake:  offline FirstFrameProvider, to run the LLM path without a model.
func NewProviderFrom(mode string, getenv func(string) string) (Provider, error) {
	timeout := 5 * time.Minute
	if s := getenv(EnvTimeout); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("llm: %s must be a positive duration such as 300s", EnvTimeout)
		}
		timeout = d
	}
	kind := strings.ToLower(strings.TrimSpace(getenv(EnvProvider)))
	switch kind {
	case "", "openai", "anthropic":
	default:
		return nil, fmt.Errorf("llm: %s=%q: want openai or anthropic", EnvProvider, kind)
	}
	var base string
	switch mode {
	case "", "off":
		return nil, ErrOff
	case "fake":
		return &FirstFrameProvider{}, nil
	case "api":
		if kind == "anthropic" {
			return newAnthropic(getenv, timeout)
		}
		base = getenv(EnvURL)
		if base == "" {
			return nil, fmt.Errorf("llm: mode api needs %s", EnvURL)
		}
		if getenv(EnvKey) == "" && getenv(EnvOpenAIKey) == "" {
			return nil, fmt.Errorf("llm: mode api needs %s (set it in the environment)", EnvKey)
		}
	case "local":
		if kind == "anthropic" {
			return nil, fmt.Errorf("llm: %s=anthropic is a hosted API: use -llm api", EnvProvider)
		}
		base = getenv(EnvURL)
		if base == "" {
			base = DefaultLocalURL
		}
		if !isLoopbackURL(base) {
			return nil, fmt.Errorf("llm: mode local needs a loopback URL, %s is not (use -llm api)", EnvURL)
		}
	default:
		return nil, fmt.Errorf("llm: unknown mode %q (want off, local or api)", mode)
	}
	model := getenv(EnvModel)
	if model == "" {
		return nil, fmt.Errorf("llm: %s is not set", EnvModel)
	}
	key := getenv(EnvKey)
	if key == "" && mode == "api" {
		key = getenv(EnvOpenAIKey)
	}
	p := NewHTTPProvider(base, model, key)
	p.Client.Timeout = timeout
	if t := getenv(EnvTemperature); t != "" {
		v, err := strconv.ParseFloat(t, 64)
		if err != nil || v < 0 || v > 2 {
			return nil, fmt.Errorf("llm: %s must be a number in [0,2]", EnvTemperature)
		}
		p.Temperature = v
	}
	return p, nil
}

func newAnthropic(getenv func(string) string, timeout time.Duration) (Provider, error) {
	key := getenv(EnvKey)
	if key == "" {
		key = getenv(EnvAnthropicKey)
	}
	if key == "" {
		return nil, fmt.Errorf("llm: %s=anthropic needs %s or %s in the environment", EnvProvider, EnvKey, EnvAnthropicKey)
	}
	base, model := getenv(EnvURL), getenv(EnvModel)
	if base == "" {
		base = DefaultAnthropicURL
	}
	if model == "" {
		model = DefaultAnthropicModel
	}
	p := NewAnthropicProvider(base, model, key)
	p.Client.Timeout = timeout
	switch e := getenv(EnvEffort); e {
	case "", "low", "medium", "high", "xhigh", "max":
		p.Effort = e
	default:
		return nil, fmt.Errorf("llm: %s=%q: want low, medium, high, xhigh or max", EnvEffort, e)
	}
	switch f := getenv(EnvFallback); f {
	case "", "off":
	case "default":
		p.Fallback = f
	default:
		return nil, fmt.Errorf("llm: %s=%q: want default or off", EnvFallback, f)
	}
	return p, nil
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// FakeProvider returns canned answers in turn (cycling), for tests and for
// running the pipeline without a model.
type FakeProvider struct {
	Responses []string
	Err       error // returned instead of an answer when set

	mu      sync.Mutex
	calls   int
	prompts []string
}

// Complete implements Provider.
func (f *FakeProvider) Complete(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, prompt)
	n := f.calls
	f.calls++
	if f.Err != nil {
		return "", f.Err
	}
	if len(f.Responses) == 0 {
		return "", nil
	}
	return f.Responses[n%len(f.Responses)], nil
}

// Calls is the number of Complete calls so far.
func (f *FakeProvider) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Prompts returns a copy of every prompt received.
func (f *FakeProvider) Prompts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.prompts...)
}
