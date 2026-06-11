package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRankVideos_FiltersAndOrders(t *testing.T) {
	in := videosResp{Results: []videoResult{
		{Key: "teaser1", Name: "Teaser", Site: "YouTube", Type: "Teaser", Official: true},
		{Key: "vimeo", Name: "Elsewhere", Site: "Vimeo", Type: "Trailer", Official: true},
		{Key: "clip", Name: "Clip", Site: "YouTube", Type: "Clip", Official: true},
		{Key: "fan", Name: "Fan trailer", Site: "YouTube", Type: "Trailer", Official: false},
		{Key: "official", Name: "Official Trailer", Site: "YouTube", Type: "Trailer", Official: true},
		{Key: "", Name: "No key", Site: "YouTube", Type: "Trailer", Official: true},
	}}
	got := rankVideos(in)
	if len(got) != 3 {
		t.Fatalf("expected 3 videos (official, fan, teaser), got %d: %+v", len(got), got)
	}
	if got[0].Key != "official" || got[1].Key != "fan" || got[2].Key != "teaser1" {
		t.Fatalf("wrong order: %+v", got)
	}
}

func TestVideos_HappyPathAndValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := videosResp{Results: []videoResult{
			{Key: "abc123", Name: "Official Trailer", Site: "YouTube", Type: "Trailer", Official: true},
		}}
		_ = json.NewEncoder(w).Encode(&resp)
	}))
	defer srv.Close()
	c := testClient(t, srv)

	videos, err := c.Videos(context.Background(), "movie", 42)
	if err != nil {
		t.Fatalf("Videos: %v", err)
	}
	if len(videos) != 1 || videos[0].Key != "abc123" {
		t.Fatalf("unexpected videos: %+v", videos)
	}

	if _, err := c.Videos(context.Background(), "book", 42); err == nil {
		t.Fatal("expected error for invalid kind")
	}
	if _, err := c.Videos(context.Background(), "movie", 0); err == nil {
		t.Fatal("expected error for invalid id")
	}
}

func TestVideos_DisabledAndUpstreamError(t *testing.T) {
	c := &Client{} // no api key
	if _, err := c.Videos(context.Background(), "movie", 1); err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c2 := testClient(t, srv)
	if _, err := c2.Videos(context.Background(), "tv", 9); err == nil {
		t.Fatal("expected error on upstream 500")
	}
}
