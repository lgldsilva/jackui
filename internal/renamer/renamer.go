package renamer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luizg/jackui/internal/ai"
	"github.com/luizg/jackui/internal/tmdb"
)

type PreviewResult struct {
	OriginalName string `json:"originalName"`
	CleanName    string `json:"cleanName"` // TMDB official name
	TargetPath   string `json:"targetPath"` // Organized Plex path (relative to sharedDir)
	Kind         string `json:"kind"`       // "movie" | "tv"
	Year         int    `json:"year,omitempty"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	EpisodeName  string `json:"episodeName,omitempty"`
}

// GeneratePreview takes a raw filename (e.g. "Star.Wars.Episode.III.2005.1080p.mkv") and constructs the target Plex-style organized path.
func GeneratePreview(ctx context.Context, aiClient *ai.Client, tmdbClient *tmdb.Client, rawName string) (*PreviewResult, error) {
	ext := filepath.Ext(rawName)
	baseNoExt := strings.TrimSuffix(rawName, ext)

	// 1. AI Extraction
	meta, _, err := aiClient.ExtractRenameMetadata(ctx, baseNoExt)
	if err != nil {
		return nil, fmt.Errorf("ai: %w", err)
	}

	// 2. TMDB Lookup (Enrichment)
	var cleanTitle string
	var year int
	kind := meta.Kind

	if tmdbClient != nil {
		match, _ := tmdbClient.Match(ctx, meta.Title)
		if match != nil {
			cleanTitle = match.Title
			year = match.Year
			kind = match.Kind
		}
	}

	// Fallback to AI values if TMDB lookup fails
	if cleanTitle == "" {
		cleanTitle = meta.Title
		year = meta.Year
	}

	// Sanitize title for file systems
	cleanTitle = sanitizeFilename(cleanTitle)

	// 3. Organização de Pastas
	var targetPath string
	var epName string

	if kind == "tv" {
		seasonNum := meta.Season
		if seasonNum <= 0 {
			seasonNum = 1 // Default to season 1
		}
		episodeNum := meta.Episode

		// If we have TMDB, try to fetch the localized episode title
		if tmdbClient != nil && episodeNum > 0 {
			// Find TMDB ID by match again if we have it
			match, _ := tmdbClient.Match(ctx, cleanTitle)
			if match != nil {
				epName = tmdbClient.FetchEpisodeName(ctx, match.TmdbID, seasonNum, episodeNum)
			}
		}

		// Sanitize episode name
		epName = sanitizeFilename(epName)

		// Format season/episode: SXXEXX
		sStr := fmt.Sprintf("S%02d", seasonNum)
		eStr := fmt.Sprintf("E%02d", episodeNum)

		// Series structure: Series/Show Name/Season XX/Show Name - SXXEXX - Episode Name.ext
		if episodeNum > 0 {
			var filename string
			if epName != "" {
				filename = fmt.Sprintf("%s - %s%s - %s%s", cleanTitle, sStr, eStr, epName, ext)
			} else {
				filename = fmt.Sprintf("%s - %s%s%s", cleanTitle, sStr, eStr, ext)
			}
			targetPath = filepath.Join("Series", cleanTitle, fmt.Sprintf("Season %02d", seasonNum), filename)
		} else {
			targetPath = filepath.Join("Series", cleanTitle, fmt.Sprintf("Season %02d", seasonNum), rawName)
		}
	} else {
		// Movie structure: Filmes/Movie Name (Year)/Movie Name (Year).ext
		var folderName string
		if year > 0 {
			folderName = fmt.Sprintf("%s (%d)", cleanTitle, year)
		} else {
			folderName = cleanTitle
		}
		targetPath = filepath.Join("Filmes", folderName, folderName+ext)
	}

	return &PreviewResult{
		OriginalName: rawName,
		CleanName:    cleanTitle,
		TargetPath:   targetPath,
		Kind:         kind,
		Year:         year,
		Season:       meta.Season,
		Episode:      meta.Episode,
		EpisodeName:  epName,
	}, nil
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
