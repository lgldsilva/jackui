package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// RenameMetadata holds the structured metadata extracted from a raw torrent/release
// filename by the AI rename prompt.
type RenameMetadata struct {
	Title        string `json:"title"`
	Year         int    `json:"year"`
	Kind         string `json:"kind"` // "movie" | "tv"
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	EpisodeTitle string `json:"episode_title"`
}

func (c *Client) ExtractRenameMetadata(ctx context.Context, rawName string) (*RenameMetadata, string, error) {
	return c.ExtractRenameMetadataWithContext(ctx, rawName, "")
}

// ExtractRenameMetadataWithContext is ExtractRenameMetadata plus an optional
// taxonomy hint. The USER message stays the bare rawName (so title extraction
// is unaffected and the benchmark/parser behaviour is identical); the hint is
// appended to the SYSTEM prompt as an extra instruction telling the model which
// destination category folders already exist and to prefer reusing one. An
// empty hint is the exact legacy call.
func (c *Client) ExtractRenameMetadataWithContext(ctx context.Context, rawName, taxonomyHint string) (*RenameMetadata, string, error) {
	if c == nil {
		return nil, "", errors.New("ai client not initialized")
	}
	system := renameSystem
	if taxonomyHint != "" {
		system = renameSystem + "\n\n" + taxonomyHint
	}
	var lastErr error
	for _, s := range c.slotList() {
		if !c.breaker.available(s.Provider, s.ID) {
			continue
		}
		content, _, _, err := c.chat(ctx, s, system, rawName, true)
		if err != nil {
			lastErr = err
			c.noteChainFailure(s, err)
			continue
		}
		c.breaker.recordSuccess(s.Provider, s.ID)
		res, perr := parseRenameJSON(content)
		if perr == nil && res != nil && res.Title != "" {
			return res, s.ID, nil
		}
	}
	return nil, "", lastErr
}

// parseTitleJSON pulls the JSON object out of a model reply (possibly wrapped in
// prose or ```json fences). When a weaker free model ignores the JSON format and
// replies with just the title text, fall back to using that line as the title —
// better a usable title than a hard failure.
func parseTitleJSON(content string) (*TitleResult, error) {
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start >= 0 && end > start {
		var res TitleResult
		if err := json.Unmarshal([]byte(content[start:end+1]), &res); err == nil {
			res.Title = strings.TrimSpace(res.Title)
			if res.Title != "" {
				if res.Kind == "" {
					res.Kind = "unknown"
				}
				return &res, nil
			}
		}
	}
	// Fallback: take the first non-empty line, stripped of quotes/markdown. Only
	// accept short, title-like text (reject multi-sentence prose).
	for _, line := range strings.Split(content, "\n") {
		t := strings.Trim(strings.TrimSpace(line), "`\"' #*-")
		if t != "" && len(t) <= 80 && !strings.Contains(t, ". ") {
			return &TitleResult{Title: t, Kind: "unknown"}, nil
		}
	}
	return nil, fmt.Errorf("ai: no usable title in reply")
}

// renameSystem is the production extraction prompt: it drives the AI rename
// feature AND the title cleaning before TMDB, and is exactly what the benchmark
// scores (metadataWithSlot). The few-shot examples must NEVER reuse a raw from
// DefaultBenchmarkCases — a model could copy the answer straight from its own
// prompt and inflate the benchmark (guarded by TestDefaultCasesNotInPrompts).
const renameSystem = `You extract structured metadata from raw torrent/release filenames (movies, TV episodes, season packs, anime, documentaries, live events, music, adult scenes) for organized file naming.

Reply with ONLY one raw JSON object, no prose, no code fences:
{"title": "", "year": 0, "kind": "movie" or "tv", "season": 0, "episode": 0, "episode_title": ""}

Field rules:
- "title": the clean canonical title.
  - Strip ALL technical noise: resolution (720p/1080p/2160p/4K/UHD), source (BluRay/REMUX/WEB-DL/WEBRip/HDTV/AMZN/NF/HULU/HMAX/CR), codec (x264/x265/H.264/HEVC/AV1/XviD/10bit), audio (DDP5.1/DD+/DTS/Atmos/AAC/FLAC/320kbps), HDR/DV/HDR10+, edition tags (REPACK/PROPER/EXTENDED/UNRATED/REMASTERED/Director's Cut/Final Cut/Special Edition/COMPLETE/PPV), language/dub tags (DUAL/MULTI/DUBLADO/LEGENDADO/NACIONAL/FRENCH/GERMAN/SPANISH/KOREAN/JAPANESE), bracketed ids/checksums, file extensions, leading website tags ("www.Site.com - ", "[ Site.xx ]") and the trailing release group.
  - Replace dots/underscores with spaces; restore natural capitalization; KEEP the title's own punctuation, accents and language exactly — never translate ("Divertida Mente 2" stays "Divertida Mente 2").
  - KEEP numbers that belong to the title: "Blade Runner 2049", "Wonder Woman 1984", "1917", "2012", "UFC 300".
  - TV/anime: the title is the SERIES name only — never include SxxEyy, episode numbers, "Season N" or "COMPLETE" in it.
  - Anime: use the romanized title as written in the filename.
  - Music: "Artist - Album" when both are present, else just the artist.
  - Live events (UFC/F1/WWE): keep the event name, number and bout/session ("UFC 299 O'Malley vs Vera 2").
  - Adult scene "studio.YY.MM.DD.performer.name.scene.description.XXX": produce "Studio - Performer Name - Scene Description" — keep the performer and scene description, never collapse to just the studio. If in doubt, keep more detail.
- "year": the RELEASE year (integer, 0 if unknown). It is the standalone 4-digit year next to the quality tags, NOT a number that is part of the title. Adult date tokens like "24.03.15" mean 2024.
- "kind": "tv" for series/anime episodes and season packs, else "movie".
- "season"/"episode": integers, only for tv (else 0). "S03E07" or "3x07" → season 3, episode 7. A season pack ("S01", "Season 1", "S01.COMPLETE") → season set, episode 0. Anime with absolute numbering ("[Group] Title - 05") → episode 5, season 0 unless explicit.
- "episode_title": only when explicitly present in the filename, else "".

Examples:
The.Dark.Knight.2008.1080p.BluRay.x264-REFiNED → {"title":"The Dark Knight","year":2008,"kind":"movie","season":0,"episode":0,"episode_title":""}
Class.of.1999.1990.720p.BluRay.x264-SADPANDA → {"title":"Class of 1999","year":1990,"kind":"movie","season":0,"episode":0,"episode_title":""}
The.Sopranos.S02E04.Commendatori.720p.HDTV.x264 → {"title":"The Sopranos","year":0,"kind":"tv","season":2,"episode":4,"episode_title":"Commendatori"}
True.Detective.S01.COMPLETE.1080p.BluRay.x264-DEMAND → {"title":"True Detective","year":0,"kind":"tv","season":1,"episode":0,"episode_title":""}
[SubsPlease] Yofukashi no Uta - 03 (1080p) [1A2B3C4D].mkv → {"title":"Yofukashi no Uta","year":0,"kind":"tv","season":0,"episode":3,"episode_title":""}
Central.do.Brasil.1998.NACIONAL.1080p.BluRay.x264-TROPiX → {"title":"Central do Brasil","year":1998,"kind":"movie","season":0,"episode":0,"episode_title":""}
www.TamilMV.re - Mission.Impossible.Fallout.2018.1080p.WEB-DL → {"title":"Mission Impossible Fallout","year":2018,"kind":"movie","season":0,"episode":0,"episode_title":""}
Daft.Punk.Random.Access.Memories.2013.FLAC.24bit → {"title":"Daft Punk - Random Access Memories","year":2013,"kind":"movie","season":0,"episode":0,"episode_title":""}
StudioX.23.05.20.Jane.Doe.Golden.Hour.XXX.1080p.MP4-WRB → {"title":"StudioX - Jane Doe - Golden Hour","year":2023,"kind":"movie","season":0,"episode":0,"episode_title":""}`

func parseRenameJSON(content string) (*RenameMetadata, error) {
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start >= 0 && end > start {
		var res RenameMetadata
		if err := json.Unmarshal([]byte(content[start:end+1]), &res); err == nil {
			res.Title = strings.TrimSpace(res.Title)
			if res.Title != "" {
				if res.Kind != "movie" && res.Kind != "tv" {
					res.Kind = "movie" // fallback
				}
				return &res, nil
			}
		}
	}
	// Fallback to simple parse using the generic parseTitleJSON fallback
	tr, err := parseTitleJSON(content)
	if err == nil && tr != nil {
		return &RenameMetadata{
			Title: tr.Title,
			Year:  tr.Year,
			Kind:  tr.Kind,
		}, nil
	}
	return nil, fmt.Errorf("ai: no usable rename metadata in reply")
}
