package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// Video is one YouTube trailer/teaser attached to a TMDB title.
type Video struct {
	Key      string `json:"key"` // YouTube video id
	Name     string `json:"name"`
	Type     string `json:"type"` // "Trailer" | "Teaser"
	Official bool   `json:"official"`
}

type videoResult struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Site     string `json:"site"`
	Type     string `json:"type"`
	Official bool   `json:"official"`
}

type videosResp struct {
	Results []videoResult `json:"results"`
}

// Videos returns the YouTube trailers/teasers for a movie or tv id, best first
// (official trailers → trailers → teasers). include_video_language widens the
// pt-BR query to English uploads — most titles only have EN trailers.
func (c *Client) Videos(ctx context.Context, kind string, tmdbID int) ([]Video, error) {
	if c.apiKey == "" {
		return nil, ErrDisabled
	}
	if kind != "movie" && kind != "tv" {
		return nil, fmt.Errorf("tmdb videos: invalid kind %q", kind)
	}
	if tmdbID <= 0 {
		return nil, fmt.Errorf("tmdb videos: invalid id %d", tmdbID)
	}
	q := url.Values{}
	q.Set("api_key", c.apiKey)
	q.Set("language", "pt-BR")
	q.Set("include_video_language", "pt,en,null")
	endpoint := apiBase + "/" + kind + "/" + strconv.Itoa(tmdbID) + "/videos?" + q.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb videos %s/%d returned %d", kind, tmdbID, resp.StatusCode)
	}
	var out videosResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return rankVideos(out), nil
}

// rankVideos keeps YouTube trailers/teasers and orders them best-first. Pure →
// unit-testable without a live TMDB.
func rankVideos(out videosResp) []Video {
	videos := []Video{}
	for _, r := range out.Results {
		if r.Site != "YouTube" || r.Key == "" {
			continue
		}
		if r.Type != "Trailer" && r.Type != "Teaser" {
			continue
		}
		videos = append(videos, Video{Key: r.Key, Name: r.Name, Type: r.Type, Official: r.Official})
	}
	sort.SliceStable(videos, func(i, j int) bool {
		return videoRank(videos[i]) < videoRank(videos[j])
	})
	return videos
}

func videoRank(v Video) int {
	switch {
	case v.Type == "Trailer" && v.Official:
		return 0
	case v.Type == "Trailer":
		return 1
	default:
		return 2
	}
}
