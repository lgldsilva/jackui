package transmissionrpc

import (
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// session-set aplica alt-speed + queue + speed-limits (estado do handler observável).
func TestMethodSessionSet(t *testing.T) {
	s := streamer.NewForTesting()
	h := NewHandler(nil, s, nil, "/data", "/data")
	r := h.methodSessionSet(map[string]interface{}{
		keyAltSpeedEn: true, keyAltSpeedDown: float64(100), keyAltSpeedUp: float64(50),
		keyStartAdded: false,
		keyDLQueueEn:  true, keyDLQueueSize: float64(7),
		keySeedQueueEn: true, keySeedQueueSize: float64(3),
		keySpeedLimitDown: float64(800), keySpeedLimitDownEn: true,
	})
	if r.Result != "success" {
		t.Fatalf("session-set: %q", r.Result)
	}
	if !h.altSpeedEnabled || h.altSpeedDown != 100 || h.altSpeedUp != 50 || h.startAddedTorrents {
		t.Error("alt-speed não aplicou")
	}
	if !h.downloadQueueEnabled || h.downloadQueueSize != 7 || !h.seedQueueEnabled || h.seedQueueSize != 3 {
		t.Error("queue não aplicou")
	}
	if down, _ := s.RateLimits(); down != 800*1024/8 {
		t.Errorf("speed limit não aplicou: %d", down)
	}
}

func TestResolveCategory(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	if got := h.resolveCategory("/data/Filmes", []string{"Series"}); got != "Series" {
		t.Errorf("resolveCategory(labels) = %q, want Series", got)
	}
	// sem labels → deriva do downloadDir (não vazio)
	if got := h.resolveCategory("/data/Filmes", nil); got == "" {
		t.Error("resolveCategory sem labels deveria derivar do dir")
	}
}

func TestMethodGroupGetSet(t *testing.T) {
	s := streamer.NewForTesting()
	h := NewHandler(nil, s, nil, "/data", "/data")
	if r := h.methodGroupGet(map[string]interface{}{}); r.Result != "success" {
		t.Errorf("group-get: %q", r.Result)
	}
	if r := h.methodGroupGet(map[string]interface{}{"name": "Outro"}); r.Result != "success" {
		t.Errorf("group-get(filtro): %q", r.Result)
	}
	if r := h.methodGroupSet(map[string]interface{}{}); r.Result == "success" {
		t.Error("group-set sem name deveria falhar")
	}
	if r := h.methodGroupSet(map[string]interface{}{"name": "Default", keySpeedLimitDown: float64(800)}); r.Result != "success" {
		t.Errorf("group-set Default: %q", r.Result)
	}
}

// parseKbps: kbps do Transmission → bytes/seg (v*1024/8); 0 p/ ausente/tipo errado.
func TestParseKbps(t *testing.T) {
	if got := parseKbps(map[string]interface{}{"x": float64(800)}, "x"); got != 800*1024/8 {
		t.Errorf("parseKbps(800) = %d, want %d", got, 800*1024/8)
	}
	if got := parseKbps(map[string]interface{}{}, "x"); got != 0 {
		t.Errorf("parseKbps(ausente) = %d, want 0", got)
	}
	if got := parseKbps(map[string]interface{}{"x": "nope"}, "x"); got != 0 {
		t.Errorf("parseKbps(não-float) = %d, want 0", got)
	}
}

// applySessionSpeedLimits aplica os caps de banda no streamer (kbps → bytes/s).
func TestApplySessionSpeedLimits(t *testing.T) {
	s := streamer.NewForTesting()
	h := NewHandler(nil, s, nil, "/data", "/data")
	h.applySessionSpeedLimits(map[string]interface{}{
		keySpeedLimitDown: float64(800), keySpeedLimitDownEn: true,
		keySpeedLimitUp: float64(400), keySpeedLimitUpEn: true,
	})
	down, up := s.RateLimits()
	if down != 800*1024/8 || up != 400*1024/8 {
		t.Errorf("rate limits = %d/%d, want %d/%d", down, up, 800*1024/8, 400*1024/8)
	}
	// down setado mas DESABILITADO → zera (cobre os ramos de enabled=false)
	s2 := streamer.NewForTesting()
	NewHandler(nil, s2, nil, "/data", "/data").applySessionSpeedLimits(map[string]interface{}{
		keySpeedLimitDown: float64(800), keySpeedLimitDownEn: false,
		keySpeedLimitUpEn: false,
	})
	// streamer nil → no-op sem panic
	(&Handler{}).applySessionSpeedLimits(map[string]interface{}{})
}

func TestQueuePos(t *testing.T) {
	if queuePos(downloads.Download{Status: downloads.StatusCompleted}) != 0 {
		t.Error("completed → 0")
	}
	if queuePos(downloads.Download{Status: downloads.StatusFailed}) != 0 {
		t.Error("failed → 0")
	}
	if got := queuePos(downloads.Download{ID: 7, Status: downloads.StatusDownloading}); got != 7 {
		t.Errorf("downloading → ID(7), got %d", got)
	}
}

// torrent-set deve ACEITAR todos os campos que os *arr mandam (alguns são no-op),
// sem erro. Com streamer nil, os campos dependentes de streamer caem nos guards
// (sem panic) e os baseados em store (paused/labels) aplicam.
func TestTorrentSet_AcceptsAllArgs(t *testing.T) {
	st := newTestStore(t)
	h := NewHandler(st, nil, nil, "/data", "/data")
	id := mkDownload(t, st, strings.Repeat("a", 40), downloads.StatusDownloading)

	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":                []interface{}{float64(id)},
		"paused":             true,
		"labels":             []interface{}{"Filmes"},
		"bandwidthPriority":  float64(1),
		"sequentialDownload": true,
		"peerLimit":          float64(50),
		"trackerList":        "udp://tracker.example:80/announce",
		"trackerAdd":         []interface{}{"udp://add.example:80"},
		"trackerReplace":     []interface{}{[]interface{}{float64(0), "udp://new.example:80"}},
		"downloadLimit":      float64(1000), "downloadLimited": true,
		"uploadLimit": float64(500), "uploadLimited": true,
		"seedRatioLimit":      float64(2),
		"seedIdleLimit":       float64(30),
		"queuePosition":       float64(0),
		"honorsSessionLimits": false,
	})
	if resp.Result != "success" {
		t.Fatalf("torrent-set: %q", resp.Result)
	}
	// paused=true aplicou no store (observável)
	got, err := st.Get(1, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != downloads.StatusPaused {
		t.Errorf("status = %q, want %q (paused deveria ter aplicado)", got.Status, downloads.StatusPaused)
	}
}

// Com NewForTesting (streamer sem client), os apply* que dependem do client devem
// cair nos guards de client-nil sem panic.
func TestTorrentSet_StreamerWithoutClient(t *testing.T) {
	st := newTestStore(t)
	s := streamer.NewForTesting()
	h := NewHandler(st, s, nil, "/data", "/data")
	id := mkDownload(t, st, strings.Repeat("b", 40), downloads.StatusDownloading)
	resp := h.methodTorrentSet(map[string]interface{}{
		"ids":                []interface{}{float64(id)},
		"sequentialDownload": true,
		"peerLimit":          float64(30),
		"bandwidthPriority":  float64(-1),
	})
	if resp.Result != "success" {
		t.Errorf("torrent-set (streamer sem client): %q", resp.Result)
	}
}

func TestMethodTorrentVerifyAndReannounce(t *testing.T) {
	st := newTestStore(t)
	s := streamer.NewForTesting()
	h := NewHandler(st, s, nil, "/data", "/data")
	id := mkDownload(t, st, strings.Repeat("c", 40), downloads.StatusDownloading)
	args := map[string]interface{}{"ids": []interface{}{float64(id)}}

	if r := h.methodTorrentVerify(args); r.Result != "success" {
		t.Errorf("torrent-verify: %q", r.Result)
	}
	if r := h.methodTorrentReannounce(args); r.Result != "success" {
		t.Errorf("torrent-reannounce: %q", r.Result)
	}
	// streamer nil → reannounce ainda dá success (no-op)
	if r := (&Handler{}).methodTorrentReannounce(args); r.Result != "success" {
		t.Errorf("torrent-reannounce (streamer nil): %q", r.Result)
	}
}

// buildPeers/buildPieces sem torrentObj ativo → vazio (não panica).
func TestBuildPeersPieces_NoTorrent(t *testing.T) {
	if peers := buildPeers(torrentView{}); len(peers) != 0 {
		t.Errorf("buildPeers sem torrent = %d, want 0", len(peers))
	}
	if pieces := buildPieces(torrentView{}); pieces != "" {
		t.Errorf("buildPieces sem torrent = %q, want \"\"", pieces)
	}
}

func TestActiveTorrentObjects_Empty(t *testing.T) {
	dls := []downloads.Download{{InfoHash: strings.Repeat("d", 40)}}
	// streamer nil → mapa vazio
	if m := (&Handler{}).activeTorrentObjects(dls); len(m) != 0 {
		t.Errorf("activeTorrentObjects(streamer nil) = %d, want 0", len(m))
	}
	// NewForTesting (client nil) → mapa vazio
	h := NewHandler(nil, streamer.NewForTesting(), nil, "/data", "/data")
	if m := h.activeTorrentObjects(dls); len(m) != 0 {
		t.Errorf("activeTorrentObjects(client nil) = %d, want 0", len(m))
	}
}

// free-space confina o path: dentro do downloadDir ok; fora cai no fallback.
func TestMethodFreeSpace_Confine(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	for _, p := range []string{"", "/data/sub", "/etc/passwd", "../../root"} {
		if r := h.methodFreeSpace(map[string]interface{}{"path": p}); r.Result != "success" {
			t.Errorf("free-space(%q) = %q, want success", p, r.Result)
		}
	}
}

func TestConfinePathEdges(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	if _, ok := h.confinePath(""); ok {
		t.Error("confinePath(\"\") deveria ser false")
	}
	if _, ok := h.confinePath("/etc"); ok {
		t.Error("confinePath(/etc) fora do root deveria ser false")
	}
	if got, ok := h.confinePath("/data/x/y"); !ok || got != "/data/x/y" {
		t.Errorf("confinePath(/data/x/y) = (%q,%v), want (/data/x/y,true)", got, ok)
	}
}

func TestFetchTorrentHash_BadScheme(t *testing.T) {
	if _, err := fetchTorrentHash("ftp://nope/x.torrent"); err == nil {
		t.Error("fetchTorrentHash com esquema não-http deveria falhar")
	}
	if _, err := fetchTorrentHash("magnet:?xt=urn:btih:abc"); err == nil {
		t.Error("fetchTorrentHash com magnet deveria falhar (não é http)")
	}
}

// Helpers puros de derivação (ETA, ratio, stall) — lógica de cálculo.
func TestComputeHelpers(t *testing.T) {
	// (1000-400)/100 = 6
	if got := computeETA(torrentView{downRate: 100, totalSize: 1000, d: downloads.Download{BytesDownloaded: 400}}); got != 6 {
		t.Errorf("computeETA = %d, want 6", got)
	}
	if got := computeETA(torrentView{}); got != -1 {
		t.Errorf("computeETA(zero) = %d, want -1", got)
	}
	if got := computeETAIdle(torrentView{upRate: 10}); got != 0 {
		t.Errorf("computeETAIdle(up>0) = %d, want 0", got)
	}
	if got := computeETAIdle(torrentView{}); got != -1 {
		t.Errorf("computeETAIdle(idle) = %d, want -1", got)
	}
	if got := computeRatio(torrentView{uploadedBytes: 50, downloadedBytes: 100}); got != 0.5 {
		t.Errorf("computeRatio = %v, want 0.5", got)
	}
	if got := computeRatio(torrentView{}); got != 0.0 {
		t.Errorf("computeRatio(zero) = %v, want 0", got)
	}
	stalledView := torrentView{totalSize: 1000, d: downloads.Download{BytesDownloaded: 10}}
	if !isStalled(downloads.Download{Status: downloads.StatusDownloading}, stalledView) {
		t.Error("isStalled deveria ser true (downloading, downRate 0, falta baixar)")
	}
	if isStalled(downloads.Download{Status: downloads.StatusCompleted}, stalledView) {
		t.Error("isStalled não deveria ser true p/ completed")
	}
}

// addTorrentFilename resolve magnet / infoHash cru / rejeita o resto (sem rede).
func TestAddTorrentFilename(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data")
	hash := strings.Repeat("a", 40)
	if ih, _, err := h.addTorrentFilename("magnet:?xt=urn:btih:" + hash); err != nil || ih == "" {
		t.Errorf("magnet: ih=%q err=%v", ih, err)
	}
	if ih, mg, err := h.addTorrentFilename(hash); err != nil || ih != hash || !strings.HasPrefix(mg, "magnet:") {
		t.Errorf("hash cru: ih=%q mg=%q err=%v", ih, mg, err)
	}
	if _, _, err := h.addTorrentFilename("não-é-torrent"); err == nil {
		t.Error("filename não suportado deveria falhar")
	}
}

// Cobre o ramo default do parseIDs (tipo não-numérico, ex. "recently-active").
func TestParseIDs_NonNumericDefault(t *testing.T) {
	if parseIDs("recently-active") != nil {
		t.Error("parseIDs(string) deveria cair no default (nil)")
	}
	if parseIDs([]interface{}{"x", true}) != nil && len(parseIDs([]interface{}{"x", true})) != 0 {
		t.Error("parseIDs(lista sem float) deveria dar set vazio")
	}
}

// mapJackUIStatusToTR: mapeia status JackUI → status do Transmission (int).
func TestMapJackUIStatusToTR(t *testing.T) {
	cases := []struct {
		si   *streamer.TorrentInfo
		d    downloads.Download
		want int
	}{
		{&streamer.TorrentInfo{Status: "paused"}, downloads.Download{}, 0},
		{&streamer.TorrentInfo{Status: "seeding"}, downloads.Download{}, 6},
		{&streamer.TorrentInfo{Status: "downloading", Progress: 0.5}, downloads.Download{}, 4},
		{&streamer.TorrentInfo{Status: "downloading"}, downloads.Download{}, 3},
		{nil, downloads.Download{Status: downloads.StatusQueued}, 3},
		{nil, downloads.Download{Status: downloads.StatusDownloading, Progress: 0.5}, 4},
		{nil, downloads.Download{Status: downloads.StatusDownloading, Progress: 1.0}, 6},
		{nil, downloads.Download{Status: downloads.StatusCompleted}, 6},
		{nil, downloads.Download{Status: downloads.StatusPaused}, 0},
		{nil, downloads.Download{Status: downloads.StatusFailed}, 0},
		{nil, downloads.Download{Status: "weird"}, 0},
	}
	for i, c := range cases {
		if got := mapJackUIStatusToTR(c.d, c.si); got != c.want {
			t.Errorf("case %d: got %d, want %d", i, got, c.want)
		}
	}
}

func TestAddTorrentMetainfo_Errors(t *testing.T) {
	h := NewHandler(nil, nil, nil, "/data", "/data") // streamer nil → caminho metainfo.Load
	if _, _, _, err := h.addTorrentMetainfo("!!! não é base64 !!!"); err == nil {
		t.Error("base64 inválido deveria falhar")
	}
	if _, _, _, err := h.addTorrentMetainfo("aGVsbG8="); err == nil { // "hello" — base64 ok, torrent não
		t.Error("metainfo inválido deveria falhar")
	}
}

// start/stop: completed/failed são pulados; downloading muda de status.
func TestMethodTorrentStartStop(t *testing.T) {
	st := newTestStore(t)
	s := streamer.NewForTesting()
	h := NewHandler(st, s, nil, "/data", "/data")
	dl := mkDownload(t, st, strings.Repeat("e", 40), downloads.StatusDownloading)
	done := mkDownload(t, st, strings.Repeat("f", 40), downloads.StatusCompleted)
	ids := map[string]interface{}{"ids": []interface{}{float64(dl), float64(done)}}

	if r := h.methodTorrentStop(ids); r.Result != "success" {
		t.Fatalf("stop: %q", r.Result)
	}
	if got, _ := st.Get(1, dl); got.Status != downloads.StatusPaused {
		t.Errorf("dl status = %q, want paused", got.Status)
	}
	if got, _ := st.Get(1, done); got.Status != downloads.StatusCompleted {
		t.Errorf("completed deveria ser pulado, status = %q", got.Status)
	}
	if r := h.methodTorrentStart(ids); r.Result != "success" {
		t.Fatalf("start: %q", r.Result)
	}
	// torrent-start re-queues; scheduler promotes later (active limit).
	if got, _ := st.Get(1, dl); got.Status != downloads.StatusQueued {
		t.Errorf("dl status após start = %q, want queued", got.Status)
	}
}
