package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAnthropicRoundTrip(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		hdr = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		io.WriteString(w, `{"content":[{"type":"thinking","thinking":""},{"type":"text","text":"{\"locations\":[]}"}],`+
			`"stop_reason":"end_turn","usage":{"input_tokens":120,"output_tokens":30}}`)
	}))
	defer srv.Close()
	p := NewAnthropicProvider(srv.URL+"/v1/", "claude-opus-5-5", "sk-ant-test")
	p.Effort = "high"
	out, err := p.Complete(context.Background(), "hello")
	if err != nil || out != `{"locations":[]}` {
		t.Fatalf("%q %v", out, err)
	}
	if hdr.Get("x-api-key") != "sk-ant-test" || hdr.Get("anthropic-version") == "" || hdr.Get("anthropic-beta") != "" {
		t.Errorf("headers %v", hdr)
	}
	if _, ok := got["temperature"]; ok {
		t.Error("temperature must not be sent to Anthropic")
	}
	if got["model"] != "claude-opus-5-5" || got["max_tokens"].(float64) != 16000 {
		t.Errorf("body %v", got)
	}
	if oc, _ := got["output_config"].(map[string]any); oc["effort"] != "high" {
		t.Errorf("effort %v", got["output_config"])
	}
	if _, ok := got["fallbacks"]; ok {
		t.Error("fallback must be off by default")
	}
	if u := p.Usage(); u.Requests != 1 || u.InputTokens != 120 || u.OutputTokens != 30 {
		t.Errorf("usage %+v", u)
	}
}

func TestAnthropicRefusalTruncationAndFallback(t *testing.T) {
	reply := `{"content":[],"stop_reason":"refusal","usage":{"input_tokens":5,"output_tokens":0}}`
	var beta string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		beta = r.Header.Get("anthropic-beta")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		io.WriteString(w, reply)
	}))
	defer srv.Close()
	p := NewAnthropicProvider(srv.URL, "m", "k")
	if _, err := p.Complete(context.Background(), "x"); !errors.Is(err, ErrRefused) {
		t.Errorf("refusal: %v", err)
	}
	reply = `{"content":[{"type":"text","text":"{\"locations\":[{\"file\":\"a.go\""}],"stop_reason":"max_tokens","usage":{"input_tokens":5,"output_tokens":9}}`
	p.Fallback = "default"
	out, err := p.Complete(context.Background(), "x")
	if err != nil || !strings.HasPrefix(out, `{"locations"`) {
		t.Fatalf("truncated reply must be returned for the tolerant parser: %q %v", out, err)
	}
	if p.Usage().Truncated != 1 {
		t.Errorf("usage %+v", p.Usage())
	}
	if beta != "server-side-fallback-2026-07-01" || body["fallbacks"] != "default" {
		t.Errorf("fallback not sent: %q %v", beta, body["fallbacks"])
	}
}

func TestAnthropicErrorsAndRetry(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad model"}}`)
	}))
	defer srv.Close()
	p := NewAnthropicProvider(srv.URL, "m", "SECRETKEY")
	p.Backoff = time.Millisecond
	_, err := p.Complete(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "bad model") || strings.Contains(err.Error(), "SECRETKEY") {
		t.Fatalf("err %v", err)
	}
	if n != 2 {
		t.Errorf("want 1 retry after 429, got %d requests", n)
	}
}

func TestNewProviderAnthropicAndFake(t *testing.T) {
	p, err := NewProviderFrom("api", env(map[string]string{EnvProvider: "anthropic", EnvAnthropicKey: "k"}))
	if err != nil {
		t.Fatal(err)
	}
	ap := p.(*AnthropicProvider)
	if ap.BaseURL != DefaultAnthropicURL || ap.Model != DefaultAnthropicModel || ap.Key != "k" {
		t.Errorf("%+v", ap)
	}
	bad := []map[string]string{
		{EnvProvider: "anthropic"},
		{EnvProvider: "anthropic", EnvKey: "k", EnvEffort: "huge"},
		{EnvProvider: "anthropic", EnvKey: "k", EnvFallback: "yes"},
		{EnvProvider: "gemini", EnvKey: "k"},
		{EnvProvider: "anthropic", EnvKey: "k", EnvTimeout: "soon"},
	}
	for _, m := range bad {
		if _, err := NewProviderFrom("api", env(m)); err == nil {
			t.Errorf("accepted %v", m)
		}
	}
	if _, err := NewProviderFrom("local", env(map[string]string{EnvProvider: "anthropic", EnvModel: "m"})); err == nil {
		t.Error("local + anthropic accepted")
	}
	p, err = NewProviderFrom("api", env(map[string]string{EnvURL: "https://x/v1", EnvModel: "m", EnvOpenAIKey: "k", EnvTimeout: "30s"}))
	if err != nil || p.(*HTTPProvider).Key != "k" || p.(*HTTPProvider).Client.Timeout != 30*time.Second {
		t.Fatalf("openai key fallback / timeout: %v %+v", err, p)
	}
	if p, err := NewProviderFrom("fake", env(nil)); err != nil || NameOf(p) != "fake:first-frame" {
		t.Errorf("fake: %v %v", p, err)
	}
}

// The offline provider goes through BuildPrompt, the tolerant parser and the
// vote, and names the first user frame of a real stack.
func TestFirstFrameProviderThroughTriage(t *testing.T) {
	stack, err := os.ReadFile(filepath.Join("testdata", "stacks", "C1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	f := &FirstFrameProvider{}
	in := Input{Stack: string(stack), Files: map[string]string{"crash/crash.go": strings.Repeat("// x\n", 30)}}
	out, err := Triage(context.Background(), f, in, TriageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Calls() != DefaultRuns || len(out.Findings) == 0 || out.Findings[0].Location.File != "crash/crash.go" {
		t.Fatalf("calls %d, findings %+v, notes %v", f.Calls(), out.Findings, out.Notes)
	}
}

func TestUsageSubAndCost(t *testing.T) {
	u := Usage{Requests: 3, InputTokens: 2_000_000, OutputTokens: 500_000}.Sub(Usage{Requests: 1, InputTokens: 1_000_000})
	if u.Requests != 2 || u.InputTokens != 1_000_000 {
		t.Errorf("%+v", u)
	}
	if c := u.Cost(4, 20); c != 14 {
		t.Errorf("cost %v", c)
	}
}

func TestParseConfigs(t *testing.T) {
	cs, err := ParseConfigs("all")
	if err != nil || len(cs) != 4 {
		t.Fatalf("%v %v", cs, err)
	}
	cs, err = ParseConfigs(" st , SM+rank, st ")
	if err != nil || len(cs) != 2 || cs[0].Name() != "ST" || cs[1].Name() != "SM+RoSFM" {
		t.Fatalf("%v %v", cs, err)
	}
	if cs, err := ParseConfigs("default"); err != nil || cs[0] != DefaultConfig() {
		t.Fatalf("%v %v", cs, err)
	}
	for _, bad := range []string{"", "xx", "st+foo"} {
		if _, err := ParseConfigs(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestParseStackFramesKeepsMethodReceiver(t *testing.T) {
	st := "goroutine 1 [running]:\ngoflsample/crash.(*Registry).Inc(0xc000012345, {0x1, 0x2})\n\t/x/crash/crash.go:29 +0x1d\n" +
		"goflsample/crash.Fanout.func1()\n\t/x/crash/hard.go:144 +0x1\npanic({0x7ff, 0x7ff})\n\t/go/src/runtime/panic.go:783 +0x132\n"
	fs := ParseStackFrames(st)
	if len(fs) != 3 || fs[0].Func != "goflsample/crash.(*Registry).Inc" || fs[1].Func != "goflsample/crash.Fanout.func1" || fs[2].Func != "panic" {
		t.Fatalf("%+v", fs)
	}
}
