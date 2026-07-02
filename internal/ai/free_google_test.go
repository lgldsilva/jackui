package ai

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

// TestIsFreeModel_Google guards the money-sensitive rule: only the pinned free Gemini
// ids count as free; a paid look-alike (gemini-3.5-flash) or a pro model must NOT, or
// the benchmark would spend real credits on it.
func TestIsFreeModel_Google(t *testing.T) {
	c := &Client{googleFree: config.DefaultFreeModels("google")}
	if !c.isFreeModel("google", "gemini-2.5-flash-lite") {
		t.Error("gemini-2.5-flash-lite should be free")
	}
	if c.isFreeModel("google", "gemini-3.5-flash") {
		t.Error("gemini-3.5-flash is PAID — must not be free")
	}
	if c.isFreeModel("google", "gemini-2.5-pro") {
		t.Error("gemini-2.5-pro is paid — must not be free")
	}
}

// TestIsFreeModel_GoogleConfigOverride: a config-provided free list is honored, so a
// model absent from the default but present in the override counts as free.
func TestIsFreeModel_GoogleConfigOverride(t *testing.T) {
	c := &Client{googleFree: []string{"gemini-custom-free"}}
	if !c.isFreeModel("google", "gemini-custom-free") {
		t.Error("config-overridden free id should be free")
	}
	if c.isFreeModel("google", "gemini-2.5-flash-lite") {
		t.Error("a model NOT in the override list should not be free")
	}
}

// TestIsFreeModel_OtherProviders confirms the existing behavior is intact after adding
// the Google branch.
func TestIsFreeModel_OtherProviders(t *testing.T) {
	c := &Client{}
	if !c.isFreeModel("groq", "llama-3.1-8b-instant") {
		t.Error("groq is a free-tier provider")
	}
	if !c.isFreeModel("ollama", "llama3.1:8b") {
		t.Error("ollama is a free-tier provider")
	}
	if !c.isFreeModel("openrouter", "meta-llama/llama-3.3-70b-instruct:free") {
		t.Error(":free suffix should be free")
	}
	if c.isFreeModel("openrouter", "anthropic/claude-3.5-sonnet") {
		t.Error("a metered model without a free marker should not be free")
	}
}
