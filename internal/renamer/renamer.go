package renamer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/parser"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

type PreviewResult struct {
	OriginalName string `json:"originalName"`
	CleanName    string `json:"cleanName"`  // TMDB official name
	TargetPath   string `json:"targetPath"` // Organized Plex path (relative to sharedDir)
	Kind         string `json:"kind"`       // "movie" | "tv"
	Year         int    `json:"year,omitempty"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	EpisodeName  string `json:"episodeName,omitempty"`
}

// GeneratePreview takes a raw filename (e.g. "Star.Wars.Episode.III.2005.1080p.mkv") and constructs the target Plex-style organized path.
//
// Robustness: the regex parser (internal/parser) runs first and provides the
// season/episode/year as trustworthy hints. The AI handles the messy title; if
// it fails or returns nothing, we fall back to a regex-derived title so a
// rename NEVER hard-errors. The regex S/E OVERRIDES the AI's — it's reliable and
// gives series coherence (every "Show.S01E0x" lands in Season 1 consistently,
// no per-file AI drift), while TMDB normalizes the title across episodes.
func GeneratePreview(ctx context.Context, aiClient *ai.Client, tmdbClient *tmdb.Client, rawName string) (*PreviewResult, error) {
	ext := filepath.Ext(rawName)
	baseNoExt := strings.TrimSuffix(rawName, ext)
	parsed := parser.Parse(baseNoExt)

	// 1. AI Extraction — with a regex fallback so it never hard-fails.
	meta, _, err := aiClient.ExtractRenameMetadata(ctx, baseNoExt)
	if err != nil || meta == nil || meta.Title == "" {
		meta = fallbackMetadata(baseNoExt, parsed)
	}

	// Regex S/E is reliable — prefer it over the AI's (the AI sometimes drifts
	// the season/episode). A regex-detected S/E also forces kind=tv.
	season, episode, kind := meta.Season, meta.Episode, meta.Kind
	if parsed.Season > 0 {
		season = parsed.Season
	}
	if parsed.Episode > 0 {
		episode = parsed.Episode
	}
	if parsed.Season > 0 || parsed.Episode > 0 {
		kind = "tv"
	}

	// 2. TMDB Lookup (Enrichment)
	var cleanTitle string
	var year int

	if tmdbClient != nil {
		match, _ := tmdbClient.Match(ctx, meta.Title)
		if match != nil {
			cleanTitle = match.Title
			year = match.Year
			if kind == "" {
				kind = match.Kind
			}
		}
	}

	// Fallback to AI/parser values if TMDB lookup fails
	if cleanTitle == "" {
		cleanTitle = meta.Title
		year = meta.Year
	}
	if year == 0 {
		year = parsed.Year
	}

	// Sanitize title for file systems
	cleanTitle = sanitizeFilename(cleanTitle)

	// 3. Organização de Pastas
	var epName string

	if kind == "tv" && tmdbClient != nil && episode > 0 {
		seasonNum := season
		if seasonNum <= 0 {
			seasonNum = 1
		}
		if match, _ := tmdbClient.Match(ctx, cleanTitle); match != nil {
			epName = sanitizeFilename(tmdbClient.FetchEpisodeName(ctx, match.TmdbID, seasonNum, episode))
		}
	}

	targetPath := buildTargetPath(targetPathInput{Kind: kind, CleanTitle: cleanTitle, Year: year, Season: season, Episode: episode, EpName: epName, Ext: ext, RawName: rawName})

	return &PreviewResult{
		OriginalName: rawName,
		CleanName:    cleanTitle,
		TargetPath:   targetPath,
		Kind:         kind,
		Year:         year,
		Season:       season,
		Episode:      episode,
		EpisodeName:  epName,
	}, nil
}

// titleCutRe marks where a release title ends — the first quality/year/S-E token.
var titleCutRe = regexp.MustCompile(`(?i)[. _-](\d{3,4}p|s\d{1,2}e\d{1,3}|s\d{1,2}\b|19\d{2}|20\d{2}|bluray|web-?dl|webrip|hdtv|x26[45]|h\.?26[45]|hevc|remux|dvdrip)`)

// fallbackMetadata derives rename metadata from the regex parser when the AI is
// unavailable or returns nothing — so a rename degrades gracefully instead of
// erroring. The title is everything before the first quality/year/S-E marker.
func fallbackMetadata(baseNoExt string, p parser.Quality) *ai.RenameMetadata {
	title := baseNoExt
	if loc := titleCutRe.FindStringIndex(baseNoExt); loc != nil && loc[0] > 0 {
		title = baseNoExt[:loc[0]]
	}
	title = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(title, ".", " "), "_", " "))
	kind := "movie"
	if p.Season > 0 || p.Episode > 0 {
		kind = "tv"
	}
	return &ai.RenameMetadata{Title: title, Year: p.Year, Kind: kind, Season: p.Season, Episode: p.Episode}
}

// targetPathInput groups the parameters for buildTargetPath.
type targetPathInput struct {
	Kind       string
	CleanTitle string
	Year       int
	Season     int
	Episode    int
	EpName     string
	Ext        string
	RawName    string
}

// buildTargetPath is the pure path-building step of GeneratePreview, extracted
// so it can be unit-tested without an AI or TMDB client.
func buildTargetPath(in targetPathInput) string {
	if in.Kind == "tv" {
		if in.Season <= 0 {
			in.Season = 1
		}
		sStr := fmt.Sprintf("S%02d", in.Season)
		eStr := fmt.Sprintf("E%02d", in.Episode)
		if in.Episode > 0 {
			var filename string
			if in.EpName != "" {
				filename = fmt.Sprintf("%s - %s%s - %s%s", in.CleanTitle, sStr, eStr, in.EpName, in.Ext)
			} else {
				filename = fmt.Sprintf("%s - %s%s%s", in.CleanTitle, sStr, eStr, in.Ext)
			}
			return filepath.Join("Series", in.CleanTitle, fmt.Sprintf("Season %02d", in.Season), filename)
		}
		return filepath.Join("Series", in.CleanTitle, fmt.Sprintf("Season %02d", in.Season), in.RawName)
	}
	folderName := movieLabel(in.CleanTitle, in.Year)
	return filepath.Join("Filmes", folderName, folderName+in.Ext)
}

// sequelTailRe matches a trailing sequence number ("Toy Story 3", "Rocky 4").
// Single digit 1-9 only, so years/large numbers in the title ("Blade Runner
// 2049", "Apollo 13") are NOT mistaken for a sequel number.
var sequelTailRe = regexp.MustCompile(`^(.*\S)\s+([1-9])$`)

// movieLabel formats a movie's folder/file label:
//   - sequel (number already in the title) → "Title - N" (e.g. "Toy Story - 3")
//   - otherwise                            → "Title - Year" (e.g. "Inception - 2010")
//   - no year and no sequel                → just the title
func movieLabel(title string, year int) string {
	if m := sequelTailRe.FindStringSubmatch(title); m != nil {
		return fmt.Sprintf("%s - %s", m[1], m[2])
	}
	if year > 0 {
		return fmt.Sprintf("%s - %d", title, year)
	}
	return title
}

func sanitizeFilename(s string) string {
	r := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", " -",
		"*", "",
		"?", "",
		"\"", "'",
		"<", "",
		">", "",
		"|", "-",
	)
	return strings.TrimSpace(r.Replace(s))
}

// ResolveTargetConflict checks if the target path exists and appends a numeric suffix (e.g. " (2)") to the filename if necessary to avoid conflicts.
func ResolveTargetConflict(baseDir string, targetRelPath string) string {
	fullPath := filepath.Join(baseDir, targetRelPath)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return targetRelPath
	}

	ext := filepath.Ext(targetRelPath)
	dir := filepath.Dir(targetRelPath)
	base := filepath.Base(targetRelPath)
	baseNoExt := strings.TrimSuffix(base, ext)

	counter := 2
	for {
		newRelPath := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", baseNoExt, counter, ext))
		newFullPath := filepath.Join(baseDir, newRelPath)
		if _, err := os.Stat(newFullPath); os.IsNotExist(err) {
			return newRelPath
		}
		counter++
	}
}
