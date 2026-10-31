package services

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAIModelsWithoutSelectedModel(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		name := "keyless"
		if keyed {
			name = "saved-key"
		}
		t.Run(name, func(t *testing.T) {
			svc, _ := newModelService(t, &memoryVault{})
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				wantAuth := ""
				if keyed {
					wantAuth = "Bearer " + modelKey
				}
				if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != wantAuth {
					t.Errorf("request = %s %s %v", r.Method, r.URL, r.Header)
				}
				io.WriteString(w, `{"data":[{"id":"z"},{"id":"a"}]}`)
			}))
			defer srv.Close()
			if _, err := svc.SetAIConnection("openai", srv.URL+"/v1", ""); err != nil {
				t.Fatal(err)
			}
			if keyed {
				if _, err := svc.SetAIKey(modelKey); err != nil {
					t.Fatal(err)
				}
			}
			got, err := svc.AIModels()
			if err != nil || !reflect.DeepEqual(got, []string{"a", "z"}) || calls != 1 || svc.AISettings().Model != "" {
				t.Fatalf("models = %v, calls = %d, err = %v", got, calls, err)
			}
		})
	}
}

func TestAIModelsRejectsUnboundKey(t *testing.T) {
	svc, _ := newModelService(t, &memoryVault{})
	if _, err := svc.SetAIConnection("openai", "https://saved.example/v1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetAIKey(modelKey); err != nil {
		t.Fatal(err)
	}
	// A changed on-disk endpoint must not receive a key bound to the old one.
	svc.settingsMu.Lock()
	s := svc.readSettings()
	s.AIBaseURL = "https://other.example/v1"
	err := svc.writeSettings(s)
	svc.settingsMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AIModels(); !errors.Is(err, errAINoKey) {
		t.Fatalf("unbound key = %v", err)
	}
	if _, err := svc.SetAIConnection("anthropic", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AIModels(); !errors.Is(err, errAINoKey) {
		t.Fatalf("keyless anthropic = %v", err)
	}
}

func TestAIModelsScrubsFailure(t *testing.T) {
	svc, logs := newModelService(t, &memoryVault{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"Incorrect key: `+modelKey+`"}}`)
	}))
	defer srv.Close()
	configureModel(t, svc, srv.URL)
	_, err := svc.AIModels()
	if err == nil || !strings.HasPrefix(err.Error(), "API Key 无效或没有权限：") || strings.Contains(err.Error(), modelKey) || strings.Contains(logs.String(), modelKey) {
		t.Fatalf("failure = %v, logs = %s", err, logs.String())
	}
}
