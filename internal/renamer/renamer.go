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
	// ReusedFolder is the existing destination folder the renamer matched and
	// reused instead of creating a fresh category folder ("" when it created a
	// brand-new one). Lets the UI flag "fits the existing taxonomy".
	ReusedFolder string `json:"reusedFolder,omitempty"`
}

// LocalContext describes WHERE the item lives and the taxonomy already present
// at the destination, so the renamer can land it inside an existing category
// folder instead of inventing a near-duplicate (e.g. recreating "Filmes" inside
// a library that already has "Movies"). It is cheap to build (shallow ReadDir)
// and always optional — a nil/zero LocalContext degrades to the legacy
// hardcoded "Series"/"Filmes" labels.
type LocalContext struct {
	// CurrentPath is the relative path of the item inside its mount
	// (e.g. "Movies/2024/Inception.mkv"), used only for the AI hint.
	CurrentPath string
	// MountName is the source mount's display name (AI hint only).
	MountName string
	// DestFolders are the first-level folder names already present at the
	// destination base (the promote/library target), truncated by the caller.
	DestFolders []string
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
	return GeneratePreviewWithContext(ctx, aiClient, tmdbClient, rawName, nil)
}

// GeneratePreviewWithContext is GeneratePreview plus a LocalContext: when the
// destination already has category folders ("Movies", "Series", "Anime", …),
// the final path REUSES the matching existing folder (case-insensitive +
// language synonyms) instead of recreating a near-duplicate. The AI also gets a
// short hint about the location + existing taxonomy so its kind/title is
// location-aware. A nil ctx → identical behaviour to the legacy GeneratePreview.
func GeneratePreviewWithContext(ctx context.Context, aiClient *ai.Client, tmdbClient *tmdb.Client, rawName string, lc *LocalContext) (*PreviewResult, error) {
	ext := filepath.Ext(rawName)
	baseNoExt := strings.TrimSuffix(rawName, ext)
	parsed := parser.Parse(baseNoExt)

	// 1. AI Extraction — with a regex fallback so it never hard-fails. When we
	// have a LocalContext, feed the taxonomy hint so the model can disambiguate
	// (e.g. an item already filed under "Anime"); falls back to the plain path.
	meta, _, err := extractMeta(ctx, aiClient, baseNoExt, lc)
	if err != nil || meta == nil || meta.Title == "" {
		meta = fallbackMetadata(baseNoExt, parsed)
	}

	season, episode, kind := reconcileSeasonEpisode(meta, parsed)

	// 2. TMDB Lookup (Enrichment) + sanitize for file systems. enrichTitle may
	// fill an empty kind from TMDB, so kind is reassigned here.
	cleanTitle, year, kind := enrichTitle(ctx, tmdbClient, meta, parsed, kind)
	cleanTitle = sanitizeFilename(cleanTitle)

	// 3. Episode name (TMDB) when this is an episode.
	epName := fetchEpisodeName(ctx, tmdbClient, kind, cleanTitle, season, episode)

	// Reuse an existing destination category folder when one matches the kind
	// (case-insensitive + language synonyms), else fall back to the default
	// label. reusedFolder is "" when nothing matched (a fresh folder is created).
	category, reusedFolder := resolveCategoryFolder(kind, lc)
	targetPath := buildTargetPath(targetPathInput{Category: category, Kind: kind, CleanTitle: cleanTitle, Year: year, Season: season, Episode: episode, EpName: epName, Ext: ext, RawName: rawName})

	return &PreviewResult{
		OriginalName: rawName,
		CleanName:    cleanTitle,
		TargetPath:   targetPath,
		Kind:         kind,
		Year:         year,
		Season:       season,
		Episode:      episode,
		EpisodeName:  epName,
		ReusedFolder: reusedFolder,
	}, nil
}

// reconcileSeasonEpisode prefers the regex parser's S/E over the AI's (the AI
// sometimes drifts), and a regex-detected S/E forces kind=tv for series
// coherence.
func reconcileSeasonEpisode(meta *ai.RenameMetadata, parsed parser.Quality) (season, episode int, kind string) {
	season, episode, kind = meta.Season, meta.Episode, meta.Kind
	if parsed.Season > 0 {
		season = parsed.Season
	}
	if parsed.Episode > 0 {
		episode = parsed.Episode
	}
	if parsed.Season > 0 || parsed.Episode > 0 {
		kind = "tv"
	}
	return season, episode, kind
}

// enrichTitle normalizes the title via TMDB when available, falling back to the
// AI/parser values. Returns the clean title, year and (possibly TMDB-filled)
// kind.
func enrichTitle(ctx context.Context, tmdbClient *tmdb.Client, meta *ai.RenameMetadata, parsed parser.Quality, kind string) (string, int, string) {
	var cleanTitle string
	var year int
	if tmdbClient != nil {
		if match, _ := tmdbClient.Match(ctx, meta.Title); match != nil {
			cleanTitle = match.Title
			year = match.Year
			if kind == "" {
				kind = match.Kind
			}
		}
	}
	if cleanTitle == "" {
		cleanTitle = meta.Title
		year = meta.Year
	}
	if year == 0 {
		year = parsed.Year
	}
	return cleanTitle, year, kind
}

// fetchEpisodeName looks up the TMDB episode title for a tv episode (else "").
func fetchEpisodeName(ctx context.Context, tmdbClient *tmdb.Client, kind, cleanTitle string, season, episode int) string {
	if kind != "tv" || tmdbClient == nil || episode <= 0 {
		return ""
	}
	seasonNum := season
	if seasonNum <= 0 {
		seasonNum = 1
	}
	match, _ := tmdbClient.Match(ctx, cleanTitle)
	if match == nil {
		return ""
	}
	return sanitizeFilename(tmdbClient.FetchEpisodeName(ctx, match.TmdbID, seasonNum, episode))
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
	// Category is the top-level folder label (e.g. "Movies" / "Filmes" /
	// "Series"). When empty, buildTargetPath falls back to the legacy default
	// ("Series" for tv, "Filmes" for movies).
	Category   string
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
		category := in.Category
		if category == "" {
			category = defaultTVFolder
		}
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
			return filepath.Join(category, in.CleanTitle, fmt.Sprintf("Season %02d", in.Season), filename)
		}
		return filepath.Join(category, in.CleanTitle, fmt.Sprintf("Season %02d", in.Season), in.RawName)
	}
	category := in.Category
	if category == "" {
		category = defaultMovieFolder
	}
	folderName := movieLabel(in.CleanTitle, in.Year)
	return filepath.Join(category, folderName, folderName+in.Ext)
}

// Default category labels used when no LocalContext folder matches. Kept in
// the original mixed PT/EN to preserve legacy behaviour for fresh libraries.
const (
	defaultTVFolder    = "Series"
	defaultMovieFolder = "Filmes"
)

// movieCategorySynonyms / tvCategorySynonyms are the normalized labels (and
// language variants) that mean "movies" / "tv shows". A destination folder
// whose normalized name is in the matching set is reused as-is, so we never
// create "Filmes" next to an existing "Movies" (or vice-versa).
var (
	movieCategorySynonyms = map[string]bool{
		"movies": true, "movie": true, "filmes": true, "filme": true,
		"films": true, "cinema": true, "documentaries": true, "documentarios": true,
	}
	tvCategorySynonyms = map[string]bool{
		"series": true, "serie": true, "seriados": true, "seriado": true,
		"tv": true, "tvshows": true, "shows": true, "show": true,
		"anime": true, "animes": true,
	}
)

// resolveCategoryFolder picks the top-level folder for the given kind, REUSING
// an existing destination folder whose normalized name is a known synonym for
// that kind (case- and accent-insensitive). Returns (folderToUse, reusedName):
// reusedName is the original existing folder when one matched, else "" (and the
// caller's default label is used). When several existing folders match, the
// shortest/first (sorted by the caller) wins for determinism.
func resolveCategoryFolder(kind string, lc *LocalContext) (string, string) {
	if lc == nil || len(lc.DestFolders) == 0 {
		return "", ""
	}
	syn := movieCategorySynonyms
	if kind == "tv" {
		syn = tvCategorySynonyms
	}
	for _, f := range lc.DestFolders {
		if syn[normalizeCategory(f)] {
			return f, f
		}
	}
	return "", ""
}

// normalizeCategory lower-cases, strips accents and removes spaces/punctuation
// so "TV Shows" == "tvshows", "Séries" == "series", "Filmes " == "filmes".
func normalizeCategory(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case 'á', 'à', 'â', 'ã', 'ä':
			b.WriteRune('a')
		case 'é', 'è', 'ê', 'ë':
			b.WriteRune('e')
		case 'í', 'ì', 'î', 'ï':
			b.WriteRune('i')
		case 'ó', 'ò', 'ô', 'õ', 'ö':
			b.WriteRune('o')
		case 'ú', 'ù', 'û', 'ü':
			b.WriteRune('u')
		case 'ç':
			b.WriteRune('c')
		default:
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// maxAIContextFolders caps how many existing folder names we feed the AI hint,
// so a huge library never blows up the prompt.
const maxAIContextFolders = 25

// extractMeta runs the AI rename extraction, optionally passing a compact
// location hint (current path + existing top-level folders). The USER message
// stays the bare rawName so title extraction is unaffected — the hint rides on
// the SYSTEM prompt (see ai.ExtractRenameMetadataWithContext), keeping the
// benchmark and parser behaviour identical. With no LocalContext it is the
// plain ExtractRenameMetadata call.
func extractMeta(ctx context.Context, aiClient *ai.Client, baseNoExt string, lc *LocalContext) (*ai.RenameMetadata, string, error) {
	hint := buildAIContextHint(lc)
	if hint == "" {
		return aiClient.ExtractRenameMetadata(ctx, baseNoExt)
	}
	return aiClient.ExtractRenameMetadataWithContext(ctx, baseNoExt, hint)
}

// buildAIContextHint formats the optional taxonomy instruction appended to the
// system prompt. Returns "" when there's nothing useful to add. The existing
// folders are truncated to maxAIContextFolders so a huge library never blows up
// the prompt.
func buildAIContextHint(lc *LocalContext) string {
	if lc == nil || (lc.CurrentPath == "" && len(lc.DestFolders) == 0) {
		return ""
	}
	var b strings.Builder
	b.WriteString("Destination library context (the file is being reorganized, not described):\n")
	if lc.CurrentPath != "" {
		b.WriteString("- current location of the file: ")
		if lc.MountName != "" {
			b.WriteString(lc.MountName)
			b.WriteString("/")
		}
		b.WriteString(lc.CurrentPath)
		b.WriteString("\n")
	}
	if len(lc.DestFolders) > 0 {
		folders := lc.DestFolders
		if len(folders) > maxAIContextFolders {
			folders = folders[:maxAIContextFolders]
		}
		b.WriteString("- existing top-level folders at the destination: ")
		b.WriteString(strings.Join(folders, ", "))
		b.WriteString("\n")
	}
	b.WriteString("When the destination already has a folder for this kind (case-insensitive, e.g. Movies==Filmes, Series==TV Shows==Séries, Anime), assume it will be filed there — do NOT invent a near-duplicate category. This context never changes the JSON schema: still reply with ONLY the metadata object for the filename.")
	return b.String()
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
