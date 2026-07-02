package ai

import "testing"

// TestIsFreeModel_Google guards the money-sensitive rule: only the pinned free Gemini
// ids count as free; a paid look-alike (gemini-3.5-flash) or a pro model must NOT, or
// the benchmark would spend real credits on it.
func TestIsFreeModel_Google(t *testing.T) {
	if !isFreeModel("google", "gemini-2.5-flash-lite") {
		t.Error("gemini-2.5-flash-lite should be free")
	}
	if isFreeModel("google", "gemini-3.5-flash") {
		t.Error("gemini-3.5-flash is PAID — must not be free")
	}
	if isFreeModel("google", "gemini-2.5-pro") {
		t.Error("gemini-2.5-pro is paid — must not be free")
	}
}

// TestIsFreeModel_OtherProviders confirms the existing behavior is intact after adding
// the Google branch.
func TestIsFreeModel_OtherProviders(t *testing.T) {
	if !isFreeModel("groq", "llama-3.1-8b-instant") {
		t.Error("groq is a free-tier provider")
	}
	if !isFreeModel("ollama", "llama3.1:8b") {
		t.Error("ollama is a free-tier provider")
	}
	if !isFreeModel("openrouter", "meta-llama/llama-3.3-70b-instruct:free") {
		t.Error(":free suffix should be free")
	}
	if isFreeModel("openrouter", "anthropic/claude-3.5-sonnet") {
		t.Error("a metered model without a free marker should not be free")
	}
}
