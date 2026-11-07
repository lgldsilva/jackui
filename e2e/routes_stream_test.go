//go:build e2e

package e2e

import "testing"

// TestStreamRoutes covers the torrent streamer surface: swarm reads, favorites
// folders, HLS renditions, sidecars and the admin cache/drop mutations. All
// probes use a well-formed but nonexistent info hash, so every parametrized
// route must answer with its handler's not-found/idempotent contract.
func TestStreamRoutes(t *testing.T) {
	runRoutes(t, []routeCase{
		{method: "GET", path: "/api/stream/cache", want: 200},
		{method: "DELETE", path: "/api/stream/cache", want: 200},
		{method: "GET", path: "/api/stream/rate", want: 200},
		{method: "GET", path: "/api/stream/active", want: 200},
		{method: "POST", path: "/api/stream/active/pause", want: 200},
		{method: "POST", path: "/api/stream/active/resume", want: 200},
		{method: "GET", path: "/api/stream/limits", want: 200},
		// 0 = unlimited per the handler's own contract, so an empty bind sets
		// (unlimited, unlimited) — benign, deterministic 200.
		{method: "POST", path: "/api/stream/limits", body: `{}`, want: 200},

		{method: "POST", path: "/api/stream/" + bogusHash + "/pause", want: 404},
		{method: "POST", path: "/api/stream/" + bogusHash + "/resume", want: 404},
		{method: "POST", path: "/api/stream/" + bogusHash + "/priority", body: `{}`, want: 404},
		{method: "POST", path: "/api/stream/" + bogusHash + "/files/" + bogusFile + "/priority", body: `{}`, want: 404},

		{method: "GET", path: "/api/stream/favorites", want: 200},
		{method: "POST", path: "/api/stream/favorite", body: `{}`, want: 400},
		// Unfavorite of an unknown name is idempotent → 200.
		{method: "DELETE", path: "/api/stream/favorite/" + bogusName, want: 200},
		// Empty name lists are rejected, not treated as no-ops.
		{method: "POST", path: "/api/stream/favorites/batch/remove", body: `{"names":[]}`, want: 400},
		{method: "POST", path: "/api/stream/favorites/batch/folder", body: `{"names":[]}`, want: 400},
		{method: "GET", path: "/api/stream/favorites/folders", want: 200},
		{method: "POST", path: "/api/stream/favorites/folders", body: `{}`, want: 400},
		// Folder mutations on an unknown id answer silent 204 (idempotent).
		{method: "PATCH", path: "/api/stream/favorites/folders/" + bogusID, body: `{}`, want: 204},
		{method: "DELETE", path: "/api/stream/favorites/folders/" + bogusID, want: 204},
		{method: "PATCH", path: "/api/stream/favorite/" + bogusName + "/folder", body: `{}`, want: 204},

		{method: "POST", path: "/api/stream/import", body: `{}`, want: 400},
		{method: "POST", path: "/api/stream/add", body: `{}`, want: 400},
		// multipart with no file part → bind must reject.
		{method: "POST", path: "/api/stream/add-file", rawCT: "multipart/form-data; boundary=e2ebound", body: "--e2ebound--", want: 400},

		{method: "GET", path: "/api/stream/info/" + bogusHash, want: 404},
		{method: "GET", path: "/api/stream/probe/" + bogusHash + "/" + bogusFile, want: 502},
		{method: "GET", path: "/api/stream/audio/meta/" + bogusHash + "/" + bogusFile, want: 200},
		{method: "GET", path: "/api/stream/subtrack/" + bogusHash + "/" + bogusFile + "/" + bogusFile, want: 502},
		// 500 with an empty body is the pinned contract for a dead playlist.
		{method: "GET", path: "/api/stream/playlist/" + bogusHash + "/" + bogusFile, want: 500},
		{method: "POST", path: "/api/stream/prefetch/" + bogusHash + "/" + bogusFile, want: 404},
		{method: "GET", path: "/api/stream/artwork/" + bogusHash + "/" + bogusFile, want: 404},
		// Metadata of an unknown hash is a cached empty record → 200.
		{method: "GET", path: "/api/stream/metadata/" + bogusHash, want: 200},
		{method: "POST", path: "/api/stream/metadata/batch", body: `{"hashes":[]}`, want: 400},
		// Health of an unknown torrent is {active:false, known:false} → 200.
		{method: "GET", path: "/api/stream/health/" + bogusHash, want: 200},
		{method: "POST", path: "/api/stream/health/batch", body: `{"hashes":[]}`, want: 400},
		{method: "GET", path: "/api/stream/trackers/" + bogusHash, want: 200},
		{method: "GET", path: "/api/stream/thumb/" + bogusHash + "/" + bogusFile, want: 404},
		{method: "GET", path: "/api/stream/art/" + bogusHash, want: 204},
		{method: "POST", path: "/api/stream/art/resolve/batch", body: `{}`, want: 400},
		{method: "POST", path: "/api/stream/art/" + bogusHash + "/resolve", body: `{}`, want: 204},
		{method: "GET", path: "/api/stream/" + bogusHash + "/" + bogusFile, want: 404},
		// Viewer leases are idempotent: open/release of an unknown hash → 200.
		{method: "POST", path: "/api/stream/" + bogusHash + "/viewer", body: `{}`, want: 200},
		{method: "DELETE", path: "/api/stream/" + bogusHash + "/viewer", want: 200},
		{method: "GET", path: "/api/stream/transcode/" + bogusHash + "/" + bogusFile, want: 404},
		{method: "POST", path: "/api/stream/drop/batch", body: `{"hashes":[]}`, want: 400},
		// Dropping an unknown torrent is idempotent → 200 "dropped".
		{method: "DELETE", path: "/api/stream/" + bogusHash, want: 200},

		{method: "GET", path: "/api/stream/sidecars/" + bogusHash + "/" + bogusFile, want: 502},
		{method: "GET", path: "/api/stream/sidecar/" + bogusHash + "/" + bogusFile, want: 502},

		// --- HLS renditions: no session for a bogus hash → 500 (empty body),
		// except the subtitle mini-playlist, which is generated without a
		// session and references the subtrack endpoint.
		{method: "GET", path: "/api/stream/hls/" + bogusHash + "/" + bogusFile + "/index.m3u8", want: 500},
		{method: "GET", path: "/api/stream/hls/" + bogusHash + "/" + bogusFile + "/v/0/index.m3u8", want: 500},
		{method: "GET", path: "/api/stream/hls/" + bogusHash + "/" + bogusFile + "/v/0/" + bogusSeg, want: 500},
		{method: "GET", path: "/api/stream/hls/" + bogusHash + "/" + bogusFile + "/a/0/index.m3u8", want: 500},
		{method: "GET", path: "/api/stream/hls/" + bogusHash + "/" + bogusFile + "/a/0/" + bogusSeg, want: 500},
		{method: "GET", path: "/api/stream/hls/" + bogusHash + "/" + bogusFile + "/sub/0/index.m3u8", want: 200},
		{method: "GET", path: "/api/stream/hls/" + bogusHash + "/" + bogusFile + "/" + bogusSeg, want: 500},
		{method: "GET", path: "/api/local/hls/index.m3u8", want: 400},
		{method: "GET", path: "/api/local/hls/seg", want: 400},

		{method: "GET", path: "/api/transcode/active", want: 200},
		// Killing an unknown transcode session is idempotent → 204.
		{method: "DELETE", path: "/api/transcode/active/" + bogusKey, want: 204},

		// --- universal preview viewer (torrent ?hash=&idx= or local
		// ?mount=&path= source); no source params → 400 from every route.
		{method: "GET", path: "/api/preview/archive", want: 400},
		{method: "GET", path: "/api/preview/archive/entry", want: 400},
		{method: "GET", path: "/api/preview/comic", want: 400},
		{method: "GET", path: "/api/preview/comic/page", want: 400},
		{method: "GET", path: "/api/preview/epub", want: 400},
		{method: "GET", path: "/api/preview/epub/chapter", want: 400},
		{method: "GET", path: "/api/preview/epub/res", want: 400},
	})
}
