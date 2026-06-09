package handlers

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
)

// Rótulos legíveis das categorias (consts p/ não duplicar literais — go:S1192).
const (
	labelMovies   = "Filmes"
	labelTV       = "Séries"
	labelMusic    = "Música"
	labelGames    = "Jogos"
	labelSoftware = "Software"
	labelAdult    = "Adulto"
	labelBooks    = "Livros"
	labelOther    = "Outros"
)

// CategoryResult is what the classify endpoint returns.
type CategoryResult struct {
	Category   string  `json:"category"`   // "movies" | "tv" | "music" | "games" | "software" | "adult" | "other"
	Label      string  `json:"label"`      // human-readable: "Filmes" | "Séries" | …
	Source     string  `json:"source"`     // "regex" | "ai" | "fallback"
	Confidence float64 `json:"confidence"` // 0..1
}

var categoryPatterns = []struct {
	re     *regexp.Regexp
	cat    string
	label  string
	weight float64
}{
	{regexp.MustCompile(`(?i)\b(1080p|2160p|4k|bluray|web-dl|webrip|brrip|hdr|dv|x264|x265|hevc|avc| remux)\b`), "movies", labelMovies, 0.4},
	{regexp.MustCompile(`(?i)\b(movie|film|cinema|feature)\b`), "movies", labelMovies, 0.8},
	{regexp.MustCompile(`(?i)\bS\d{1,2}E\d{1,2}\b`), "tv", labelTV, 0.9},
	{regexp.MustCompile(`(?i)\b(season|episode|s\d{1,2}|e\d{1,2})\b`), "tv", labelTV, 0.6},
	{regexp.MustCompile(`(?i)\b(complete.*series|tv.*pack|show)\b`), "tv", labelTV, 0.7},
	{regexp.MustCompile(`(?i)\b(flac|mp3|aac|album|discography|lossless|320kbps)\b`), "music", labelMusic, 0.7},
	{regexp.MustCompile(`(?i)\b(music|song|concert|live|ost|soundtrack)\b`), "music", labelMusic, 0.5},
	{regexp.MustCompile(`(?i)\b(ps4|ps5|xbox|switch|pc.*game|multi\d+|nsz|xci|nsp)\b`), "games", labelGames, 0.7},
	{regexp.MustCompile(`(?i)\b(game|repack|gog|fitgirl|dodi|codex|plaza)\b`), "games", labelGames, 0.5},
	{regexp.MustCompile(`(?i)\b(xxx|adult|porn|18\+|onlyfans)\b`), "adult", labelAdult, 0.9},
	{regexp.MustCompile(`(?i)\b(software|app|program|windows|macos|linux|crack|keygen|portable)\b`), "software", labelSoftware, 0.6},
	{regexp.MustCompile(`(?i)\.(pdf|epub|mobi|djvu|cbr|cbz)\b`), "books", labelBooks, 0.8},
}

func classifyCategory(title string) CategoryResult {
	best := CategoryResult{Category: "other", Label: labelOther, Source: "fallback", Confidence: 0}
	title = strings.TrimSpace(title)
	if title == "" {
		return best
	}
	// Highest-weight matching pattern wins; Source flips to "regex" on any hit.
	for _, p := range categoryPatterns {
		if p.re.MatchString(title) && p.weight > best.Confidence {
			best = CategoryResult{Category: p.cat, Label: p.label, Source: "regex", Confidence: p.weight}
		}
	}
	return best
}

// prefixMapping maps Jackett category IDs to our categories.
var prefixMapping = map[string]string{
	"2000": "movies",
	"5000": "tv",
	"3000": "music",
	"4000": "games",
	"4500": "software",
	"6000": "adult",
	"7000": "other",
	"8000": "books",
}

var labelMapping = map[string]string{
	"movies":   labelMovies,
	"tv":       labelTV,
	"music":    labelMusic,
	"games":    labelGames,
	"software": labelSoftware,
	"adult":    labelAdult,
	"books":    labelBooks,
	"other":    labelOther,
}

func jackettCategoryToCategory(jackettCat string) string {
	if len(jackettCat) >= 4 {
		if c, ok := prefixMapping[jackettCat[:4]]; ok {
			return c
		}
	}
	if len(jackettCat) >= 3 {
		if c, ok := prefixMapping[jackettCat[:3]+"0"]; ok {
			return c
		}
	}
	if len(jackettCat) >= 1 {
		if c, ok := prefixMapping[jackettCat[:1]+"000"]; ok {
			return c
		}
	}
	return ""
}

// ClassifyCategory returns a suggested category for a given title.
// Uses regex heuristics and optionally the AI client.
func ClassifyCategory(aiClient *ai.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		title := c.Query("title")
		jackettCat := c.DefaultQuery("jackett_category", "")
		if title == "" && jackettCat == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title or jackett_category required"})
			return
		}
		c.JSON(http.StatusOK, resolveCategory(c.Request.Context(), aiClient, title, jackettCat))
	}
}

// resolveCategory runs the classification chain — first hit wins:
// Jackett category ID → high-confidence regex → AI → any regex match → fallback.
func resolveCategory(ctx context.Context, aiClient *ai.Client, title, jackettCat string) CategoryResult {
	if res, ok := categoryFromJackett(jackettCat); ok {
		return res
	}
	regexRes, hasRegex := categoryFromRegex(title)
	if hasRegex && regexRes.Confidence >= 0.8 {
		return regexRes
	}
	if res, ok := categoryFromAI(ctx, aiClient, title); ok {
		return res
	}
	if hasRegex {
		return regexRes // any positive-confidence match beats the fallback
	}
	return CategoryResult{Category: "other", Label: labelOther, Source: "fallback", Confidence: 0}
}

// categoryFromJackett maps a Jackett category ID to our category (highest trust).
func categoryFromJackett(jackettCat string) (CategoryResult, bool) {
	if jackettCat == "" {
		return CategoryResult{}, false
	}
	cat := jackettCategoryToCategory(jackettCat)
	if cat == "" {
		return CategoryResult{}, false
	}
	return CategoryResult{Category: cat, Label: labelMapping[cat], Source: "jackett", Confidence: 0.95}, true
}

// categoryFromRegex runs the regex heuristics; ok is false when title is empty
// or nothing matched (Confidence 0).
func categoryFromRegex(title string) (CategoryResult, bool) {
	if title == "" {
		return CategoryResult{}, false
	}
	res := classifyCategory(title)
	return res, res.Confidence > 0
}

// categoryFromAI asks the optional AI client to identify the title.
func categoryFromAI(ctx context.Context, aiClient *ai.Client, title string) (CategoryResult, bool) {
	if aiClient == nil || title == "" {
		return CategoryResult{}, false
	}
	aiCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, _, err := aiClient.IdentifyTitle(aiCtx, title)
	if err != nil || result == nil || result.Kind == "unknown" {
		return CategoryResult{}, false
	}
	cat, label := "movies", labelMovies
	if result.Kind == "tv" {
		cat, label = "tv", labelTV
	}
	return CategoryResult{Category: cat, Label: label, Source: "ai", Confidence: 0.85}, true
}
