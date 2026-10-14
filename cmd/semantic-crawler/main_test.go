package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"semantic-crawler/internal/semantic"
)

func TestLoadSemanticEnvLoadsExplicitFile(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_API_KEY", "")
	directory := t.TempDir()
	envPath := filepath.Join(directory, ".env.semantic-crawler")
	if err := os.WriteFile(envPath, []byte("SEMANTIC_LLM_API_KEY=test-key\nSEMANTIC_LLM_MODEL=test-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := loadSemanticEnv(envPath); err != nil {
		t.Fatalf("load env: %v", err)
	}
	if os.Getenv("SEMANTIC_LLM_API_KEY") != "test-key" || os.Getenv("SEMANTIC_LLM_MODEL") != "test-model" {
		t.Fatalf("env file was not loaded")
	}
}

func TestLoadSemanticEnvReturnsHelpfulErrorForMissingExplicitFile(t *testing.T) {
	if err := loadSemanticEnv(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Fatal("expected missing explicit env file to fail")
	}
}

func TestLoadSemanticEnvLoadsDotEnvWithoutLegacyFile(t *testing.T) {
	t.Chdir(t.TempDir())
	const key = "SEMANTIC_ENV_LOAD_TEST"
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte(key+"=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadSemanticEnv(""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(key); got != "from-dotenv" {
		t.Fatalf("README .env configuration was not loaded: got %q", got)
	}
}

func TestLoadSemanticEnvReportsMalformedDefaultFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".env", []byte("SEMANTIC_LLM_MODEL=\"unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadSemanticEnv(""); err == nil {
		t.Fatal("malformed .env must fail instead of silently dropping configuration")
	}
}

func TestLoadSemanticEnvPreservesExportedValuesAndFilePrecedence(t *testing.T) {
	t.Chdir(t.TempDir())
	const key = "SEMANTIC_ENV_PRECEDENCE_TEST"
	t.Setenv(key, "exported")
	for name, value := range map[string]string{".env.semantic-crawler": "legacy", ".env": "default"} {
		if err := os.WriteFile(name, []byte(key+"="+value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := loadSemanticEnv(""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(key); got != "exported" {
		t.Fatalf("exported value was overwritten: %q", got)
	}
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	if err := loadSemanticEnv(""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(key); got != "legacy" {
		t.Fatalf("existing default-file precedence changed: %q", got)
	}
}

func TestSemanticModelTimeoutUsesDefaultAndEnvOverride(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_TIMEOUT", "")
	timeout, err := semanticModelTimeout()
	if err != nil {
		t.Fatalf("default timeout should parse: %v", err)
	}
	if timeout != 10*time.Minute {
		t.Fatalf("expected 10 minute default timeout, got %s", timeout)
	}

	t.Setenv("SEMANTIC_LLM_TIMEOUT", "15m")
	timeout, err = semanticModelTimeout()
	if err != nil {
		t.Fatalf("env timeout should parse: %v", err)
	}
	if timeout != 15*time.Minute {
		t.Fatalf("expected env timeout, got %s", timeout)
	}
}

func TestSemanticModelTimeoutRejectsInvalidEnvOverride(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_TIMEOUT", "not-a-duration")
	if _, err := semanticModelTimeout(); err == nil {
		t.Fatal("expected invalid timeout duration to fail")
	}
}

func TestSemanticModelTimeoutRejectsNonpositiveDuration(t *testing.T) {
	for _, value := range []string{"0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("SEMANTIC_LLM_TIMEOUT", value)
			if _, err := semanticModelTimeout(); err == nil {
				t.Fatal("configured timeout must be positive, not silently replaced")
			}
		})
	}
}

func TestSemanticModelRetryConfigUsesDefaultsAndEnvOverride(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_RETRIES", "")
	t.Setenv("SEMANTIC_LLM_RETRY_DELAY", "")
	retries, delay, err := semanticModelRetryConfig()
	if err != nil {
		t.Fatalf("default retry config should parse: %v", err)
	}
	if retries != 1 || delay != 5*time.Second {
		t.Fatalf("unexpected default retry config retries=%d delay=%s", retries, delay)
	}

	t.Setenv("SEMANTIC_LLM_RETRIES", "3")
	t.Setenv("SEMANTIC_LLM_RETRY_DELAY", "250ms")
	retries, delay, err = semanticModelRetryConfig()
	if err != nil {
		t.Fatalf("env retry config should parse: %v", err)
	}
	if retries != 3 || delay != 250*time.Millisecond {
		t.Fatalf("unexpected env retry config retries=%d delay=%s", retries, delay)
	}
}

func TestSemanticModelRetryConfigRejectsInvalidEnvOverride(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_RETRIES", "nope")
	if _, _, err := semanticModelRetryConfig(); err == nil {
		t.Fatal("expected invalid retry count to fail")
	}
	t.Setenv("SEMANTIC_LLM_RETRIES", "1")
	t.Setenv("SEMANTIC_LLM_RETRY_DELAY", "nope")
	if _, _, err := semanticModelRetryConfig(); err == nil {
		t.Fatal("expected invalid retry delay to fail")
	}
}

func TestSemanticModelRetryConfigRejectsNonpositiveDelay(t *testing.T) {
	for _, value := range []string{"0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("SEMANTIC_LLM_RETRIES", "1")
			t.Setenv("SEMANTIC_LLM_RETRY_DELAY", value)
			if _, _, err := semanticModelRetryConfig(); err == nil {
				t.Fatal("nonpositive retry delay must fail, not silently use another duration")
			}
		})
	}
}

func TestSemanticPreflightMaxTokensUsesDefaultAndEnvOverride(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_PREFLIGHT_MAX_TOKENS", "")
	maxTokens, err := semanticPreflightMaxTokens()
	if err != nil {
		t.Fatalf("default preflight max tokens should parse: %v", err)
	}
	if maxTokens != 4096 {
		t.Fatalf("expected default preflight max tokens, got %d", maxTokens)
	}

	t.Setenv("SEMANTIC_LLM_PREFLIGHT_MAX_TOKENS", "1024")
	maxTokens, err = semanticPreflightMaxTokens()
	if err != nil {
		t.Fatalf("env preflight max tokens should parse: %v", err)
	}
	if maxTokens != 1024 {
		t.Fatalf("expected env preflight max tokens, got %d", maxTokens)
	}
}

func TestSemanticPreflightMaxTokensRejectsInvalidEnvOverride(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_PREFLIGHT_MAX_TOKENS", "nope")
	if _, err := semanticPreflightMaxTokens(); err == nil {
		t.Fatal("expected invalid preflight max tokens to fail")
	}
	t.Setenv("SEMANTIC_LLM_PREFLIGHT_MAX_TOKENS", "0")
	if _, err := semanticPreflightMaxTokens(); err == nil {
		t.Fatal("expected non-positive preflight max tokens to fail")
	}
}

func TestSemanticModelMaxTokensUsesDefaultAndEnvOverride(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_MAX_TOKENS", "")
	maxTokens, err := semanticModelMaxTokens()
	if err != nil {
		t.Fatalf("default model max tokens should parse: %v", err)
	}
	if maxTokens != 16384 {
		t.Fatalf("expected default model max tokens, got %d", maxTokens)
	}

	t.Setenv("SEMANTIC_LLM_MAX_TOKENS", "16384")
	maxTokens, err = semanticModelMaxTokens()
	if err != nil {
		t.Fatalf("env model max tokens should parse: %v", err)
	}
	if maxTokens != 16384 {
		t.Fatalf("expected env model max tokens, got %d", maxTokens)
	}
}

func TestSemanticModelMaxTokensRejectsInvalidEnvOverride(t *testing.T) {
	t.Setenv("SEMANTIC_LLM_MAX_TOKENS", "nope")
	if _, err := semanticModelMaxTokens(); err == nil {
		t.Fatal("expected invalid model max tokens to fail")
	}
	t.Setenv("SEMANTIC_LLM_MAX_TOKENS", "0")
	if _, err := semanticModelMaxTokens(); err == nil {
		t.Fatal("expected non-positive model max tokens to fail")
	}
}

func TestResolveSourceSelectionUsesSlugAndDefaultPaths(t *testing.T) {
	root := t.TempDir()
	config := semantic.SourceConfig{
		Name:              "Sample Source",
		SeedURL:           "https://source.example.test/start/",
		AllowedHost:       "source.example.test",
		AllowedPathPrefix: "/start/",
		Scope:             "Clinical scope",
		MaxDepth:          2,
		MaxPages:          20,
		SkillVersion:      1,
		SourceSkillPath:   filepath.Join(root, "source-skills", "sample.md"),
	}
	data, err := semantic.MarshalSourceConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	selection, err := resolveSourceSelection("sample", "", "", root)
	if err != nil {
		t.Fatalf("resolve source: %v", err)
	}
	if selection.Slug != "sample" || selection.ConfigPath != filepath.Join(root, "sample.json") {
		t.Fatalf("unexpected selection: %#v", selection)
	}
	if selection.DatabasePath != "data/semantic-sample.db" {
		t.Fatalf("unexpected db path: %q", selection.DatabasePath)
	}
}
