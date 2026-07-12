package transcode

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// hlsSegDur is the fixed segment length in seconds. With forced keyframes
// every hlsSegDur seconds (VOD mode), segment N maps exactly to media time
// [N*hlsSegDur, (N+1)*hlsSegDur) — the invariant seek-restart relies on.
const hlsSegDur = 4

// encodeSpec captures everything needed to (re)launch ffmpeg for a session,
// possibly at a non-zero segment offset for seek-restart. Stored on the
// session so RestartAt can rebuild the command without the original handler.
type encodeSpec struct {
	dir        string
	inputURL   string
	encoder    string
	ffmpegPath string
	vod        bool // duration known → finite VOD: forced keyframes + seekable restart
	audioOnly  bool // pure-audio source → `-vn`, no video map, AAC HLS
	audioTrack int  // absolute stream index pra `-map 0:<n>` quando >0 (faixa escolhida); 0/-1 = primeira faixa de áudio (0:a:0?)
	// swDecode forces SOFTWARE video decode even for a HW encoder (e.g.
	// h264_nvenc): the `-hwaccel cuda` decode flags are suppressed so the decode
	// runs on CPU while NVENC still does the encode. Set when the GPU-decode
	// semaphore is at its cap, or after a CUDA_ERROR_OUT_OF_MEMORY recovery. The
	// scale/pix_fmt filters are unchanged: the NVENC path already scales in
	// software and re-uploads (see videoScaleFilter), so software-decoded frames
	// feed it identically.
	swDecode bool
	// variantHeight / variantBitrateK / variantLevel drive one rung of the ABR
	// ladder (HLS master, Phase 2). variantHeight == 0 is the LEGACY sentinel:
	// scale cap 1080p, -level:v 5.2, no -maxrate — byte-for-byte the pre-Phase-2
	// single-variant command. When variantHeight > 0 the scale caps at that
	// height, -level:v matches the tier, and -maxrate/-bufsize cap the bitrate so
	// the master's BANDWIDTH is honoured. Populated from a transcode.Variant.
	variantHeight   int
	variantBitrateK int
	variantLevel    int // H.264 level_idc (e.g. 40, 31); 0 → default "5.2"
}

// scaleHeight is the ABR-rung height for videoScaleFilterH (1080 = legacy cap).
func (e *encodeSpec) scaleHeight() int {
	if e.variantHeight <= 0 {
		return 1080
	}
	return e.variantHeight
}

// levelStr is the H.264 -level:v for this rung ("5.2" = legacy default).
func (e *encodeSpec) levelStr() string {
	if e.variantLevel <= 0 {
		return "5.2"
	}
	return fmt.Sprintf("%d.%d", e.variantLevel/10, e.variantLevel%10)
}

// maxrateArgs caps the encoder bitrate for a ladder rung (-maxrate/-bufsize),
// or nil for the legacy path (no explicit cap → encoder default rate control).
func (e *encodeSpec) maxrateArgs() []string {
	if e.variantBitrateK <= 0 {
		return nil
	}
	return []string{
		"-maxrate", fmt.Sprintf("%dk", e.variantBitrateK),
		"-bufsize", fmt.Sprintf("%dk", e.variantBitrateK*2),
	}
}

// decodeArgs returns the `-hwaccel` decode flags for this spec, or nil when
// software decode is forced (swDecode). Centralised so args/audioArgs and the
// guard tests share one notion of "what decode mode is this spec in".
func (e *encodeSpec) decodeArgs() []string {
	if e.swDecode {
		return nil
	}
	return hwDecodeArgsFor(e.encoder)
}

// usesHWDecode reports whether this spec, as configured, will launch ffmpeg
// with a hardware decoder (and therefore needs a GPU-decode semaphore slot).
// False for CPU encoders and for any spec forced to software decode.
func (e *encodeSpec) usesHWDecode() bool {
	return len(e.decodeArgs()) > 0
}

// args builds the ffmpeg argv to encode starting at segment `startSeg`. For
// VOD (seekable) sessions a non-zero startSeg adds input `-ss` plus `-copyts`
// so the emitted segments keep PTS aligned to the GLOBAL timeline — segments
// produced by different ffmpeg runs then splice without a PTS jump, which is
// what makes Safari accept the spliced stream (a PTS discontinuity, or an
// explicit EXT-X-DISCONTINUITY, makes it abort with SRC_NOT_SUPPORTED).
func (e *encodeSpec) args(startSeg int) []string {
	if e.audioOnly {
		return e.audioArgs(startSeg)
	}
	args := []string{
		ffHideBanner, ffLogLevel, "warning",
		ffSeekable, "1", ffMultipleReq, "1",
		ffProbesize, "10M", ffAnalyzeDuration, "3M",
	}
	// HW decode matching the encoder backend so frames feed the scale_* filter
	// (≤1080p + 8-bit NV12) below — required for 10-bit HDR sources, which the
	// HW h264 encoders can't ingest directly. No-op for CPU (software decode) and
	// suppressed when swDecode forces a CPU decode under GPU pressure / after a
	// CUDA-OOM recovery (the NVENC encode still runs on the GPU).
	args = append(args, e.decodeArgs()...)
	if e.vod && startSeg > 0 {
		// Input seek (before -i) so ffmpeg jumps via Range to the keyframe at
		// or before the requested time instead of decoding from byte 0.
		args = append(args, "-ss", strconv.Itoa(startSeg*hlsSegDur))
	}
	// Faixa de áudio: default = primeira (0:a:0?). Quando o cliente escolhe uma
	// faixa (índice absoluto > 0; em vídeo o áudio nunca é o stream 0), mapeia
	// 0:<n> — o WebKit/HLS hardcodava a primeira e ignorava a escolha.
	audioMap := "0:a:0?"
	if e.audioTrack > 0 {
		audioMap = fmt.Sprintf("0:%d?", e.audioTrack)
	}
	args = append(args,
		"-i", e.inputURL,
		"-map", "0:v:0", "-map", audioMap,
		"-sn", "-dn", "-map_chapters", "-1", "-map_metadata", "-1",
		"-c:v", e.encoder,
	)
	args = append(args, encoderPresetArgs(e.encoder)...)
	// HW encoders receive NV12 surfaces from their scale_* filter; -pix_fmt yuv420p
	// would clash with the hardware surface format. CPU (libx264) keeps it.
	if !isHWEncoder(e.encoder) {
		args = append(args, "-pix_fmt", "yuv420p")
	}
	args = append(args, "-profile:v", "main", "-level:v", e.levelStr())
	args = append(args, e.maxrateArgs()...)
	if e.vod {
		// Keyframe EXACTLY every hlsSegDur seconds so each segment starts on a
		// clean IDR — required for both standalone-decodable segments and for
		// seek-restart to land on a boundary. Replaces the fixed -g 60.
		args = append(args,
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", hlsSegDur),
			"-bf", "0",
		)
		// h264_nvenc IGNORES -force_key_frames on its own — it keeps using its
		// internal GOP, producing ~10s segments instead of hlsSegDur (verified
		// on the GTX 1070: seg dur 10.45s). -forced-idr 1 makes nvenc actually
		// emit an IDR at each forced point. libx264 honours -force_key_frames
		// natively, so this is nvenc-specific.
		if strings.HasSuffix(e.encoder, "_nvenc") {
			args = append(args, "-forced-idr", "1")
		}
		// Force the first emitted frame to PTS 0. Some HEVC/MKV containers start
		// at a non-zero PTS (observed: 1.4s); the encoder preserves it, leaving a
		// [0, offset] hole with no media so Safari stalls at currentTime 0 and
		// playback never starts (only the first segment buffers). `-copyts
		// -start_at_zero` did NOT fix it (start_at_zero only acts together with
		// an input -ss). The setpts/asetpts filters zero each stream's first
		// timestamp unconditionally. For a seek-restart they reset the -ss point
		// to 0 and -output_ts_offset then places it at the segment's slot.
		// Cap output at 1080p. Source 4K (2160p) MKVs would otherwise emit
		// H.264 Main @ 2160p — browsers' built-in H.264 decoders typically max
		// out at 1080p and silently refuse the stream (segments load but
		// nothing renders; user-visible symptom: "aparece tudo mas não toca").
		// scale=-2:min(1080,ih) preserves aspect ratio (width auto, multiple of
		// 2 required by yuv420p) and is a near no-op for sub-1080p sources.
		// setpts MUST come FIRST (on the decoded frames) — after scale_vaapi it
		// runs on VAAPI hwframes and silently fails to capture STARTPTS, leaving
		// the source's non-zero first PTS (e.g. 1.4s on many HEVC/MKV files). That
		// left a [0,1.4] hole so Safari/iOS stalled at currentTime 0 buffering only
		// the first segment, AND it broke seek-restart's output_ts_offset math
		// (the cascade). Zeroing up front fixes both, for every backend.
		args = append(args, "-vf", "setpts=PTS-STARTPTS,"+videoScaleFilterH(e.encoder, e.scaleHeight()), "-af", ffAfAsetptsZero)
		if startSeg > 0 {
			args = append(args, "-output_ts_offset", strconv.Itoa(startSeg*hlsSegDur))
		}
	} else {
		// EVENT/live: zera o PTS inicial AQUI também (mesmo motivo do ramo VOD —
		// fontes HEVC/MKV com PTS≠0 deixam um buraco [0,offset] e o Safari trava
		// no currentTime 0). setpts antes do scale; asetpts no áudio.
		args = append(args, "-g", "60", "-bf", "0",
			"-vf", "setpts=PTS-STARTPTS,"+videoScaleFilterH(e.encoder, e.scaleHeight()), "-af", ffAfAsetptsZero)
	}
	args = append(args,
		"-c:a", "aac", "-b:a", "192k", "-ac", "2",
		// CAUSA RAIZ do stall do Safari no t=0: o muxer MPEG-TS do ffmpeg adiciona
		// um initial_offset default de ~1.4s, então o seg_00000 sai começando em
		// 1.4s (não 0) — buraco [0,1.4] e o Safari/iOS travam em currentTime 0.
		// O setpts zera o FILTRO, mas o muxer re-adiciona o offset DEPOIS; só
		// -muxdelay 0 -muxpreload 0 zera no muxer. (Verificado por ffprobe:
		// seg0 start_time 1.423s → 0.) Resolve o VOD no Safari — não precisa live.
		"-muxdelay", "0", "-muxpreload", "0",
		"-f", "hls",
		"-hls_time", strconv.Itoa(hlsSegDur),
		"-hls_list_size", "0",
		"-hls_flags", "temp_file+independent_segments",
		// ffmpeg's own playlist stays EVENT/incremental; in VOD mode the
		// handler IGNORES it and synthesises a finite playlist from DurationSec.
		"-hls_playlist_type", "event",
		"-hls_segment_filename", filepath.Join(e.dir, "seg_%05d.ts"),
		"-start_number", strconv.Itoa(startSeg),
		"-y",
		filepath.Join(e.dir, hlsPlaylistFile),
	)
	return args
}

// audioArgs builds the ffmpeg argv for a pure-audio session: no video map
// (`-vn`), AAC HLS out. It mirrors the VOD seek-restart math of args (input
// `-ss` + `-output_ts_offset` so spliced segments keep a global PTS), and keeps
// `-muxdelay 0 -muxpreload 0` — the SAME Safari t=0 stall root cause applies to
// audio TS (the MPEG-TS muxer's ~1.4s initial offset leaves a [0,1.4] hole that
// Safari/iOS hang on). No forced keyframes/scale: audio segments split cleanly
// at any point.
func (e *encodeSpec) audioArgs(startSeg int) []string {
	args := []string{
		ffHideBanner, ffLogLevel, "warning",
		ffSeekable, "1", ffMultipleReq, "1",
		ffProbesize, "10M", ffAnalyzeDuration, "3M",
	}
	if e.vod && startSeg > 0 {
		args = append(args, "-ss", strconv.Itoa(startSeg*hlsSegDur))
	}
	// Faixa de áudio: default = primeira (0:a:0). Uma rendition alternativa
	// (EXT-X-MEDIA TYPE=AUDIO com URI) passa o índice ABSOLUTO do stream via
	// audioTrack (>0) → mapeia 0:<n>, gerando um TS só-áudio daquela faixa.
	audioMap := "0:a:0"
	if e.audioTrack > 0 {
		audioMap = fmt.Sprintf("0:%d", e.audioTrack)
	}
	args = append(args,
		"-i", e.inputURL,
		"-vn", "-map", audioMap,
		"-sn", "-dn", "-map_chapters", "-1", "-map_metadata", "-1",
		"-c:a", "aac", "-b:a", "192k", "-ac", "2",
		"-af", ffAfAsetptsZero,
	)
	if e.vod && startSeg > 0 {
		args = append(args, "-output_ts_offset", strconv.Itoa(startSeg*hlsSegDur))
	}
	args = append(args,
		"-muxdelay", "0", "-muxpreload", "0",
		"-f", "hls",
		"-hls_time", strconv.Itoa(hlsSegDur),
		"-hls_list_size", "0",
		"-hls_flags", "temp_file+independent_segments",
		"-hls_playlist_type", "event",
		"-hls_segment_filename", filepath.Join(e.dir, "seg_%05d.ts"),
		"-start_number", strconv.Itoa(startSeg),
		"-y",
		filepath.Join(e.dir, hlsPlaylistFile),
	)
	return args
}
