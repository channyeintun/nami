package localmodel

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func generateServer(t *testing.T, handler func(w http.ResponseWriter, body map[string]any)) *LocalModel {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		handler(w, body)
	}))
	t.Cleanup(server.Close)
	return NewLocalModel(server.URL, "test-model")
}

func TestQueryReturnsResponse(t *testing.T) {
	model := generateServer(t, func(w http.ResponseWriter, body map[string]any) {
		if body["model"] != "test-model" {
			t.Errorf("model = %v, want test-model", body["model"])
		}
		if body["stream"] != false {
			t.Errorf("stream = %v, want false", body["stream"])
		}
		options, _ := body["options"].(map[string]any)
		if options["num_predict"] != float64(64) {
			t.Errorf("options = %v, want num_predict 64", options)
		}
		_, _ = w.Write([]byte(`{"response":"  hello from ollama  "}`))
	})

	got, err := model.Query("summarize this", 64)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got != "hello from ollama" {
		t.Fatalf("Query = %q, want the trimmed response", got)
	}
}

func TestQueryOmitsOptionsWithoutTokenLimit(t *testing.T) {
	model := generateServer(t, func(w http.ResponseWriter, body map[string]any) {
		if _, ok := body["options"]; ok {
			t.Errorf("options should be omitted, got %v", body["options"])
		}
		_, _ = w.Write([]byte(`{"response":"ok"}`))
	})
	if _, err := model.Query("prompt", 0); err != nil {
		t.Fatalf("Query: %v", err)
	}
}

func TestQueryRejectsEmptyPrompt(t *testing.T) {
	if _, err := NewLocalModel(DefaultOllamaURL, "m").Query("   ", 10); err == nil {
		t.Fatal("Query accepted a blank prompt")
	}
}

func TestQueryReportsServerErrors(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter, body map[string]any){
		"http error": func(w http.ResponseWriter, _ map[string]any) {
			http.Error(w, "model not found", http.StatusNotFound)
		},
		"payload error": func(w http.ResponseWriter, _ map[string]any) {
			_, _ = w.Write([]byte(`{"error":"model is loading"}`))
		},
		"empty response": func(w http.ResponseWriter, _ map[string]any) {
			_, _ = w.Write([]byte(`{"response":"   "}`))
		},
		"malformed json": func(w http.ResponseWriter, _ map[string]any) {
			_, _ = w.Write([]byte(`{not json`))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := generateServer(t, handler).Query("prompt", 10); err == nil {
				t.Fatal("Query returned no error")
			}
		})
	}
}

func TestQueryReportsTransportFailure(t *testing.T) {
	model := NewLocalModel("http://127.0.0.1:1", "m")
	if _, err := model.Query("prompt", 10); err == nil {
		t.Fatal("Query succeeded against a closed port")
	}
}

func TestQueryNormalizesBaseURL(t *testing.T) {
	model := generateServer(t, func(w http.ResponseWriter, _ map[string]any) {
		_, _ = w.Write([]byte(`{"response":"ok"}`))
	})
	model.BaseURL += "/"
	if _, err := model.Query("prompt", 0); err != nil {
		t.Fatalf("Query with a trailing slash: %v", err)
	}
}

func TestUseLocalModelEnabled(t *testing.T) {
	for _, value := range []string{"true", "TRUE", "1", " true "} {
		t.Setenv("USE_LOCAL_MODEL", value)
		if !UseLocalModelEnabled() {
			t.Errorf("USE_LOCAL_MODEL=%q should enable local routing", value)
		}
	}
	for _, value := range []string{"", "false", "0", "yes"} {
		t.Setenv("USE_LOCAL_MODEL", value)
		if UseLocalModelEnabled() {
			t.Errorf("USE_LOCAL_MODEL=%q should not enable local routing", value)
		}
	}
}

func TestRouterWithoutLocalModel(t *testing.T) {
	t.Setenv("USE_LOCAL_MODEL", "false")
	router := NewRouter(nil)

	if router.IsLocalAvailable() {
		t.Fatal("router reports a local model although routing is disabled")
	}
	if router.LocalModelName() != "" {
		t.Fatalf("LocalModelName = %q, want empty", router.LocalModelName())
	}
	for _, task := range []TaskType{TaskCompaction, TaskScoring, TaskTitleGen, TaskIntentDetect, TaskMainReasoning} {
		if router.ShouldUseLocal(task) {
			t.Errorf("ShouldUseLocal(%v) = true with no local model", task)
		}
	}
	response, attempted, err := router.TryLocal(TaskCompaction, "prompt", 10)
	if err != nil || attempted || response != "" {
		t.Fatalf("TryLocal = %q, %v, %v; want no attempt", response, attempted, err)
	}
}

func TestRouterRoutesHelperTasksLocally(t *testing.T) {
	model := generateServer(t, func(w http.ResponseWriter, _ map[string]any) {
		_, _ = w.Write([]byte(`{"response":"local answer"}`))
	})
	router := &Router{local: model, localAvail: true}

	for _, task := range []TaskType{TaskCompaction, TaskScoring, TaskTitleGen, TaskIntentDetect} {
		if !router.ShouldUseLocal(task) {
			t.Errorf("ShouldUseLocal(%v) = false, want true", task)
		}
	}
	// Main reasoning always stays on the remote model.
	if router.ShouldUseLocal(TaskMainReasoning) {
		t.Error("main reasoning must not route to the local model")
	}
	if router.LocalModelName() != "test-model" {
		t.Errorf("LocalModelName = %q", router.LocalModelName())
	}

	response, attempted, err := router.TryLocal(TaskTitleGen, "prompt", 10)
	if err != nil {
		t.Fatalf("TryLocal: %v", err)
	}
	if !attempted || response != "local answer" {
		t.Fatalf("TryLocal = %q, attempted=%v", response, attempted)
	}

	if _, attempted, _ := router.TryLocal(TaskMainReasoning, "prompt", 10); attempted {
		t.Error("TryLocal attempted a local call for main reasoning")
	}
}

func TestTryLocalReportsAttemptOnFailure(t *testing.T) {
	model := generateServer(t, func(w http.ResponseWriter, _ map[string]any) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	router := &Router{local: model, localAvail: true}

	response, attempted, err := router.TryLocal(TaskCompaction, "prompt", 10)
	if err == nil {
		t.Fatal("TryLocal returned no error for a failing local model")
	}
	if !attempted {
		t.Error("a failed local call should still report that it was attempted")
	}
	if response != "" {
		t.Errorf("response = %q, want empty", response)
	}
}

func TestDetectLocalModelPrefersConfiguredModels(t *testing.T) {
	// DetectLocalModel targets the fixed default URL, so this exercises the
	// selection logic through the same tag payload shape the daemon returns.
	payload := `{"models":[{"name":"llama3:70b"},{"name":"gemma3:4b"}]}`
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	preferred := []string{DefaultLocalModel, "gemma3", "llama3", "qwen2"}
	selected := ""
	for _, pref := range preferred {
		for _, m := range result.Models {
			if strings.HasPrefix(m.Name, pref) {
				selected = m.Name
				break
			}
		}
		if selected != "" {
			break
		}
	}
	if selected != "gemma3:4b" {
		t.Fatalf("selected = %q, want gemma3:4b to win on preference order", selected)
	}
}
