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
	var epName string

	if kind == "tv" {
		seasonNum := meta.Season
		if seasonNum <= 0 {
			seasonNum = 1
		}
		episodeNum := meta.Episode

		if tmdbClient != nil && episodeNum > 0 {
			match, _ := tmdbClient.Match(ctx, cleanTitle)
			if match != nil {
				epName = sanitizeFilename(tmdbClient.FetchEpisodeName(ctx, match.TmdbID, seasonNum, episodeNum))
			}
		}
	}

	targetPath := buildTargetPath(targetPathInput{Kind: kind, CleanTitle: cleanTitle, Year: year, Season: meta.Season, Episode: meta.Episode, EpName: epName, Ext: ext, RawName: rawName})

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
	var folderName string
	if in.Year > 0 {
		folderName = fmt.Sprintf("%s (%d)", in.CleanTitle, in.Year)
	} else {
		folderName = in.CleanTitle
	}
	return filepath.Join("Filmes", folderName, folderName+in.Ext)
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
