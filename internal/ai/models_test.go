package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestListModelsOpenAI(t *testing.T) {
	for _, key := range []string{"", "sk-list-secret"} {
		t.Run(key, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.RequestURI() != "/v1/models" {
					t.Errorf("request = %s %s", r.Method, r.URL)
				}
				wantAuth := ""
				if key != "" {
					wantAuth = "Bearer " + key
				}
				if r.Header.Get("Authorization") != wantAuth || r.Header.Get("x-api-key") != "" || r.ContentLength != 0 {
					t.Errorf("unexpected auth/body: %v, %d", r.Header, r.ContentLength)
				}
				io.WriteString(w, `{"data":[{"id":"z-model"},{"id":"a-model"},{"id":"z-model"}],"next":"https://evil.invalid"}`)
			}))
			defer srv.Close()
			got, err := ListModels(context.Background(), Config{Provider: OpenAI, BaseURL: srv.URL + "/v1/", APIKey: key})
			if err != nil || !reflect.DeepEqual(got, []string{"a-model", "z-model"}) {
				t.Fatalf("models = %v, %v", got, err)
			}
		})
	}
}

func TestListModelsAnthropicPagination(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "secret" || r.Header.Get("anthropic-version") != anthropicVersion || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected request: %s %s %v", r.Method, r.URL, r.Header)
		}
		switch requests {
		case 1:
			if r.URL.RawQuery != "" {
				t.Errorf("first query = %q", r.URL.RawQuery)
			}
			io.WriteString(w, `{"data":[{"id":"z"}],"has_more":true,"last_id":"z /?&","next":"https://evil.invalid"}`)
		case 2:
			if r.URL.Query().Get("after_id") != "z /?&" {
				t.Errorf("cursor = %q", r.URL.RawQuery)
			}
			io.WriteString(w, `{"data":[{"id":"a"},{"id":"z"}],"has_more":true,"last_id":"a"}`)
		case 3:
			if r.URL.Query().Get("after_id") != "a" {
				t.Errorf("cursor = %q", r.URL.RawQuery)
			}
			io.WriteString(w, `{"data":[],"has_more":false}`)
		default:
			t.Error("unexpected extra page")
		}
	}))
	defer srv.Close()
	got, err := ListModels(context.Background(), Config{Provider: Anthropic, BaseURL: srv.URL, APIKey: "secret"})
	if err != nil || requests != 3 || !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("models = %v, requests = %d, err = %v", got, requests, err)
	}
}

func TestListModelsResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		status           int
		provider         Provider
	}{
		{"empty", `{"data":[]}`, "", 200, OpenAI},
		{"missing", `{}`, KindInvalid, 200, OpenAI},
		{"null", `{"data":null}`, KindInvalid, 200, OpenAI},
		{"not-json", `sk-list-secret`, KindInvalid, 200, OpenAI},
		{"bad-id", `{"data":[{}]}`, KindInvalid, 200, OpenAI},
		{"echoed-key", `{"data":[{"id":"sk-list-secret"}]}`, KindInvalid, 200, OpenAI},
		{"bad-data", `{"data":{}}`, KindInvalid, 200, OpenAI},
		{"auth", `{"error":{"message":"bad sk-list-secret"}}`, KindAuth, 401, OpenAI},
		{"server", `{"error":"bad sk-list-secret"}`, KindServer, 503, OpenAI},
		{"missing-cursor", `{"data":[{"id":"a"}],"has_more":true}`, KindInvalid, 200, Anthropic},
		{"empty-page", `{"data":[],"has_more":true,"last_id":"a"}`, KindInvalid, 200, Anthropic},
		{"loop", `{"data":[{"id":"a"}],"has_more":true,"last_id":"a"}`, KindInvalid, 200, Anthropic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			got, err := ListModels(context.Background(), Config{Provider: tc.provider, BaseURL: srv.URL, APIKey: "sk-list-secret"})
			if tc.kind == "" {
				if err != nil || got == nil || len(got) != 0 {
					t.Fatalf("empty result = %v, %v", got, err)
				}
				return
			}
			var failure *Error
			if !errors.As(err, &failure) || failure.Kind != tc.kind || strings.Contains(err.Error(), "sk-list-secret") {
				t.Fatalf("error = %v", err)
			}
			wantCalls := 1
			if tc.name == "loop" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Errorf("requests = %d, want %d", calls, wantCalls)
			}
		})
	}
}

func TestListModelsRejectsRedirects(t *testing.T) {
	for _, provider := range []Provider{OpenAI, Anthropic} {
		t.Run(string(provider), func(t *testing.T) {
			followed := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirected" {
					followed++
				}
				http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
			}))
			defer srv.Close()
			client := srv.Client()
			_, err := ListModels(context.Background(), Config{Provider: provider, BaseURL: srv.URL, APIKey: "secret", HTTP: client})
			var failure *Error
			if !errors.As(err, &failure) || failure.Status != 307 || followed != 0 || client.CheckRedirect != nil {
				t.Fatalf("redirect: %v, followed = %d", err, followed)
			}
		})
	}
}

func TestListModelsCancellationAndTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	cfg := Config{Provider: OpenAI, BaseURL: srv.URL}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListModels(ctx, cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := ListModels(ctx, cfg)
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != KindUnreachable {
		t.Fatalf("deadline = %v", err)
	}
}

func TestListModelsBoundsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[],"padding":"`)
		io.WriteString(w, strings.Repeat("x", maxResponse))
		io.WriteString(w, `"}`)
	}))
	defer srv.Close()
	_, err := ListModels(context.Background(), Config{Provider: OpenAI, BaseURL: srv.URL})
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != KindInvalid {
		t.Fatalf("oversized body = %v", err)
	}
}

func TestListModelsUsesProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("request = %s %s %v", r.Method, r.URL, r.Header)
		}
		io.WriteString(w, `{"data":[{"id":"proxy-model"}]}`)
	}))
	defer srv.Close()
	p, base := throughProxy(t, "models.proxy.test", srv)
	got, err := ListModels(context.Background(), Config{Provider: OpenAI, BaseURL: base, APIKey: "secret"})
	if err != nil || !reflect.DeepEqual(got, []string{"proxy-model"}) || !p.Saw("models.proxy.test") {
		t.Fatalf("models = %v, proxy saw %v, err = %v", got, p.Seen(), err)
	}
}

type modelsTransport func(*http.Request) (*http.Response, error)

func (f modelsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestListModelsBoundsRequestAndScrubsNetworkErrors(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: modelsTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > TestTimeout || time.Until(deadline) <= 0 {
			t.Errorf("unbounded request deadline: %v, %v", deadline, ok)
		}
		return nil, errors.New("network failed with sk-list-secret")
	})}
	_, err := ListModels(context.Background(), Config{Provider: OpenAI, BaseURL: "https://models.example/v1", APIKey: "sk-list-secret", HTTP: client})
	var failure *Error
	if !errors.As(err, &failure) || failure.Kind != KindUnreachable || calls != 1 || strings.Contains(err.Error(), "sk-list-secret") {
		t.Fatalf("network failure = %v, calls = %d", err, calls)
	}
}

func TestListModelsConnectionValidation(t *testing.T) {
	for _, cfg := range []Config{
		{Provider: OpenAI, BaseURL: "https://models.example/v1"},
		{Provider: Anthropic},
		{Provider: "unknown", APIKey: "secret"},
		{Provider: OpenAI, APIKey: "secret"},
		{Provider: OpenAI, BaseURL: "file:///tmp/models", APIKey: "secret"},
		{Provider: OpenAI, BaseURL: "https://user:secret@models.example/v1", APIKey: "secret"},
	} {
		_, err := ListModels(context.Background(), cfg)
		var failure *Error
		if !errors.As(err, &failure) || failure.Kind != KindConfig {
			t.Errorf("config %+v: %v", cfg, err)
		}
	}
}
