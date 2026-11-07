//go:build e2e

package e2e

import "testing"

// TestDownloadsRoutes covers the downloads queue surface. Probes use a
// nonexistent numeric id; the batch routes get empty id lists.
func TestDownloadsRoutes(t *testing.T) {
	runRoutes(t, []routeCase{
		{method: "GET", path: "/api/downloads", want: 200},
		{method: "GET", path: "/api/downloads/filtered", want: 200},
		{method: "GET", path: "/api/downloads/trackers", want: 200},
		{method: "GET", path: "/api/downloads/categories", want: 200},
		{method: "POST", path: "/api/downloads", body: `{}`, want: 400},
		{method: "POST", path: "/api/downloads/batch", body: `{}`, want: 400},
		{method: "POST", path: "/api/downloads/dedup-check", body: `{"infoHash":"` + bogusHash + `","files":[]}`, want: 400},
		{method: "POST", path: "/api/downloads/link", body: `{}`, want: 400},
		{method: "GET", path: "/api/downloads/destinations", want: 200},
		{method: "GET", path: "/api/downloads/dest/browse", want: 400},

		// Deleting an unknown download answers 204 (idempotent).
		{method: "DELETE", path: "/api/downloads/" + bogusID, want: 204},
		{method: "GET", path: "/api/downloads/" + bogusID + "/details", want: 404},
		{method: "GET", path: "/api/downloads/" + bogusID + "/peers", want: 404},
		{method: "GET", path: "/api/downloads/" + bogusID + "/sources", want: 404},
		{method: "POST", path: "/api/downloads/" + bogusID + "/recheck", want: 404},
		{method: "PATCH", path: "/api/downloads/" + bogusID + "/pause", want: 404},
		{method: "PATCH", path: "/api/downloads/" + bogusID + "/resume", want: 404},
		// priority is a string in the wire format.
		{method: "PATCH", path: "/api/downloads/" + bogusID + "/priority", body: `{"priority":"1"}`, want: 404},
		// Promote flows require JACKUI_SHARED_DIR, unset on the reference stack.
		{method: "POST", path: "/api/downloads/" + bogusID + "/promote", body: `{}`, want: 409},
		{method: "POST", path: "/api/downloads/" + bogusID + "/stop-seed", want: 404},

		{method: "GET", path: "/api/downloads/settings", want: 200},
		{method: "PATCH", path: "/api/downloads/pause-all", want: 200},
		{method: "PATCH", path: "/api/downloads/resume-all", want: 200},
		{method: "PATCH", path: "/api/downloads/batch/pause", body: `{"ids":[]}`, want: 200},
		{method: "PATCH", path: "/api/downloads/batch/resume", body: `{"ids":[]}`, want: 200},
		{method: "POST", path: "/api/downloads/batch/delete", body: `{"ids":[]}`, want: 200},
		// Unlike batch/delete, an empty stop-seed list is rejected.
		{method: "POST", path: "/api/downloads/batch/stop-seed", body: `{"ids":[]}`, want: 400},

		// Promote flows require JACKUI_SHARED_DIR → 409 on the reference stack.
		{method: "POST", path: "/api/downloads/promote", body: `{}`, want: 409},
		{method: "POST", path: "/api/downloads/promote/preview", body: `{}`, want: 409},
		{method: "GET", path: "/api/downloads/promote/browse", want: 409},
		{method: "GET", path: "/api/promote/destinations", want: 200},
	})
}

// TestLocalRoutes covers the local-library (filesystem) surface. Without
// mount/path query params every resolution route must answer 400; a
// nonexistent mount must never 200 with file data.
func TestLocalRoutes(t *testing.T) {
	runRoutes(t, []routeCase{
		{method: "GET", path: "/api/local/mounts", want: 200},
		{method: "GET", path: "/api/local/list", want: 400},
		{method: "POST", path: "/api/local/hidden", body: `{}`, want: 400},
		{method: "GET", path: "/api/local/hidden", want: 200},
		{method: "GET", path: "/api/local/file", want: 400},
		{method: "GET", path: "/api/local/transfer-status", want: 400},
		{method: "POST", path: "/api/local/cache", body: `{}`, want: 400},
		{method: "POST", path: "/api/local/cache/folder", body: `{}`, want: 400},
		{method: "GET", path: "/api/local/cache/status", want: 400},
		{method: "DELETE", path: "/api/local/cache", body: `{}`, want: 400},
		{method: "GET", path: "/api/local/thumb", want: 400},
		{method: "GET", path: "/api/local/transcode", want: 400},
		{method: "DELETE", path: "/api/local/file", body: `{}`, want: 400},
		{method: "POST", path: "/api/local/clean-empty", body: `{}`, want: 400},
		{method: "GET", path: "/api/local/duplicates", want: 400},
		{method: "POST", path: "/api/local/duplicates/delete", body: `{}`, want: 400},
		// Promote flows require JACKUI_SHARED_DIR → 409 on the reference stack.
		{method: "POST", path: "/api/local/promote", body: `{}`, want: 409},
		{method: "POST", path: "/api/local/promote/preview", body: `{}`, want: 409},
		{method: "GET", path: "/api/local/walk", want: 400},
		{method: "POST", path: "/api/local/move", body: `{}`, want: 400},
		{method: "POST", path: "/api/local/rename", body: `{}`, want: 400},
		{method: "POST", path: "/api/local/lock", body: `{}`, want: 400},
		{method: "POST", path: "/api/local/upload", rawCT: "multipart/form-data; boundary=e2ebound", body: "--e2ebound--", want: 400},
		{method: "GET", path: "/api/local/play", want: 400},
		{method: "POST", path: "/api/local/play/batch", body: `{}`, want: 400},
		{method: "GET", path: "/api/local/audio/meta", want: 400},
		{method: "GET", path: "/api/local/audio/cover", want: 400},
		{method: "GET", path: "/api/local/probe", want: 400},
		{method: "GET", path: "/api/local/sidecars", want: 400},
		{method: "GET", path: "/api/local/sidecar", want: 400},
		{method: "GET", path: "/api/local/subtrack", want: 400},
		{method: "GET", path: "/api/local/subtitles/auto", want: 400},
		// The pseudo-hash rides in the path and is decoded server-side; a
		// garbage pseudo-hash must be rejected, not resolved.
		{method: "GET", path: "/api/local/subtitles/auto/" + bogusHash + "/" + bogusFile, want: 400},
	})
}

// TestWatchlistPlaylistRoutes covers the watchlist and playlist CRUD surface
// (creation lifecycles live in flows_test.go).
func TestWatchlistPlaylistRoutes(t *testing.T) {
	runRoutes(t, []routeCase{
		{method: "GET", path: "/api/watchlists", want: 200},
		{method: "POST", path: "/api/watchlists", body: `{}`, want: 400},
		{method: "PUT", path: "/api/watchlists/" + bogusID, body: `{}`, want: 404},
		{method: "DELETE", path: "/api/watchlists/" + bogusID, want: 404},
		{method: "GET", path: "/api/watchlists/" + bogusID + "/hits", want: 404},
		{method: "POST", path: "/api/watchlists/schedule/parse", body: `{}`, want: 503},

		{method: "GET", path: "/api/playlists", want: 200},
		{method: "POST", path: "/api/playlists", body: `{}`, want: 400},
		{method: "GET", path: "/api/playlists/" + bogusID, want: 404},
		// Unlike the GET (which maps not-found to 404), the mutation handlers
		// pass the store's "playlist not found or not owned" through as 500.
		{method: "PATCH", path: "/api/playlists/" + bogusID, body: `{}`, want: 500},
		{method: "DELETE", path: "/api/playlists/" + bogusID, want: 500},
		{method: "POST", path: "/api/playlists/" + bogusID + "/items", body: `{"title":"e2e"}`, want: 500},
		{method: "DELETE", path: "/api/playlists/" + bogusID + "/items/" + bogusID, want: 500},
		{method: "PATCH", path: "/api/playlists/" + bogusID + "/items/" + bogusID, body: `{"position":0}`, want: 500},
	})
}
