#!/usr/bin/env bash
# hls-hw-matrix.sh — tests JackUI's HLS recipe per (encoder × video scenario)
# on the host's hardware, to discover what each GPU accepts and enable/disable
# parameters accordingly. Runs on any host with ffmpeg/ffprobe (Radeon/VAAPI,
# NVIDIA/NVENC, Intel/QSV, Apple/VideoToolbox, CPU/libx264).
#
# Usage:  bash hls-hw-matrix.sh [ffmpeg_path]
# Output: PASS/FAIL matrix per encoder×scenario + failure reason.
#
# The validation mirrors the authoritative requirements (Apple HLS Authoring Spec +
# ffmpeg HLS muxer): every segment decodable, starting at an IDR/keyframe,
# start_time≈0 (without the muxer's ~1.4s TS initial_offset), resolution ≤1080p, h264.
set -u
FFMPEG="${1:-ffmpeg}"
FFPROBE="$(dirname "$FFMPEG")/ffprobe"; [ -x "$FFPROBE" ] || FFPROBE=ffprobe
WORK="${TMPDIR:-/tmp}/hls-hw-matrix"
SRC="$WORK/src"; OUT="$WORK/out"
SEG=4   # hls_time (same as JackUI's hlsSegDur)
mkdir -p "$SRC" "$OUT"

echo "== host: $(uname -a)"
echo "== ffmpeg: $FFMPEG ($($FFMPEG -version 2>/dev/null | head -1))"

# ── 1) sample clips (lavfi), covering the scenarios ─────────────────────────
gen() { # name  extra_video  extra_audio  size
  local f="$SRC/$1"; [ -s "$f" ] && return
  $FFMPEG -y -hide_banner -loglevel error \
    -f lavfi -i "testsrc2=size=${4}:rate=24:duration=12" \
    -f lavfi -i "sine=frequency=440:duration=12" $2 $3 -shortest "$f" 2>/dev/null \
    && echo "  + $1" || echo "  ! failed to generate $1"
}
echo "== generating sample clips"
gen src_h264_aac.mp4   "-c:v libx264 -pix_fmt yuv420p"      "-c:a aac"  1280x720
gen src_hevc_aac.mkv   "-c:v libx265 -pix_fmt yuv420p"      "-c:a aac"  1280x720
gen src_hevc10.mkv     "-c:v libx265 -pix_fmt yuv420p10le"  "-c:a aac"  1920x1080
gen src_4k_hevc.mkv    "-c:v libx265 -pix_fmt yuv420p"      "-c:a aac"  3840x2160
gen src_h264_ac3.mkv   "-c:v libx264 -pix_fmt yuv420p"      "-c:a ac3"  1280x720

# ── 2) encoders available on the host ────────────────────────────────────────
ENCS=()
for e in h264_nvenc h264_vaapi h264_qsv h264_videotoolbox libx264; do
  $FFMPEG -hide_banner -encoders 2>/dev/null | grep -q " $e " && ENCS+=("$e")
done
echo "== available encoders: ${ENCS[*]:-none}"

# ── 3) JackUI recipe per encoder (mirrors pipeline.go/hls.go) ────────────────
hwdecode() { case "$1" in
  *_vaapi) echo "-hwaccel vaapi -hwaccel_device /dev/dri/renderD128 -hwaccel_output_format vaapi";;
  *_nvenc) echo "-hwaccel cuda";;
  *_qsv)   echo "-hwaccel qsv -hwaccel_output_format qsv";;
  *) echo "";; esac; }
scalefilter() { case "$1" in
  *_vaapi) echo "scale_vaapi=w=-2:h=min(1080\,ih):format=nv12";;
  *_qsv)   echo "scale_qsv=w=-2:h=min(1080\,ih):format=nv12";;
  *) echo "scale=-2:'min(1080,ih)',format=yuv420p";;
esac; }
ishw() { case "$1" in *_vaapi|*_nvenc|*_qsv|*_videotoolbox) return 0;; *) return 1;; esac; }

run_case() { # encoder  srcfile  -> echoes PASS/FAIL + reason
  local enc="$1" src="$SRC/$2" dir="$OUT/${1}__${2}"; rm -rf "$dir"; mkdir -p "$dir"
  # -hwaccel is an INPUT option → it MUST come before -i (same as JackUI).
  local args=(-y -hide_banner -loglevel error)
  local hd; hd=$(hwdecode "$enc"); [ -n "$hd" ] && args+=($hd)
  args+=(-i "$src" -map 0:v:0 -map 0:a:0? -sn -dn -c:v "$enc")
  case "$enc" in *_nvenc) args+=(-preset p4 -cq 23 -forced-idr 1);; *_vaapi) args+=(-qp 23);; *_qsv) args+=(-global_quality 23);; libx264) args+=(-preset veryfast -crf 23);; esac
  args+=(-profile:v main)
  ishw "$enc" || args+=(-pix_fmt yuv420p)
  args+=(-force_key_frames "expr:gte(t,n_forced*$SEG)" -bf 0
         -vf "setpts=PTS-STARTPTS,$(scalefilter "$enc")" -af asetpts=PTS-STARTPTS
         -c:a aac -b:a 192k -ac 2
         -muxdelay 0 -muxpreload 0
         -f hls -hls_time $SEG -hls_list_size 0 -hls_flags temp_file+independent_segments
         -hls_playlist_type vod -hls_segment_filename "$dir/seg_%05d.ts" "$dir/index.m3u8")
  local err; err=$("$FFMPEG" "${args[@]}" 2>&1); local rc=$?
  if [ $rc -ne 0 ]; then echo "FAIL ffmpeg rc=$rc: $(echo "$err" | tail -1 | cut -c1-80)"; return; fi
  local s0="$dir/seg_00000.ts"; [[ -s "$s0" ]] || { echo "FAIL no seg0"; return; }
  # validations (spec): start≈0, h264, ≤1080, seg0 and seg1 start on a keyframe
  local st res cod; st=$($FFPROBE -v error -select_streams v:0 -show_entries stream=start_time -of csv=p=0 "$s0" 2>/dev/null | head -1)
  res=$($FFPROBE -v error -select_streams v:0 -show_entries stream=height -of csv=p=0 "$s0" 2>/dev/null | head -1)
  cod=$($FFPROBE -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 "$s0" 2>/dev/null | head -1)
  local k0 k1; k0=$($FFPROBE -v error -read_intervals '%+#1' -select_streams v:0 -show_entries packet=flags -of csv=p=0 "$s0" 2>/dev/null | head -1)
  [ -s "$dir/seg_00001.ts" ] && k1=$($FFPROBE -v error -read_intervals '%+#1' -select_streams v:0 -show_entries packet=flags -of csv=p=0 "$dir/seg_00001.ts" 2>/dev/null | head -1)
  local why=""
  awk -v s="$st" 'BEGIN{exit !(s+0 > 0.5)}' && why="$why start=$st(>0.5,muxer-offset!)"
  [ "$cod" != "h264" ] && why="$why codec=$cod"
  [ -n "$res" ] && [ "$res" -gt 1080 ] 2>/dev/null && why="$why height=$res(>1080)"
  case "$k0" in *K*) :;; *) why="$why seg0-no-IDR";; esac
  [ -n "${k1:-}" ] && case "$k1" in *K*) :;; *) why="$why seg1-no-IDR";; esac
  if [[ -z "$why" ]]; then echo "PASS (start=$st h264 ${res}p IDR-ok)"; else echo "FAIL$why"; fi
}

# ── 4) matrix ────────────────────────────────────────────────────────────────
echo; echo "== MATRIX (encoder × scenario)"
SRCS=(src_h264_aac.mp4 src_hevc_aac.mkv src_hevc10.mkv src_4k_hevc.mkv src_h264_ac3.mkv)
printf "%-20s" "encoder\\scenario"; for s in "${SRCS[@]}"; do printf " | %-16s" "${s#src_}"; done; echo
for enc in "${ENCS[@]}"; do
  printf "%-20s" "$enc"
  for s in "${SRCS[@]}"; do r=$(run_case "$enc" "$s"); printf " | %-16s" "$(echo "$r" | cut -c1-16)"; done; echo
done
echo
echo "== details (failure reasons)"
for enc in "${ENCS[@]}"; do for s in "${SRCS[@]}"; do r=$(run_case "$enc" "$s"); echo "  [$enc / ${s#src_}] $r"; done; done
echo "== done. (PASS = HLS valid for Safari/iOS/hls.js: decodable seg, IDR, start≈0, ≤1080p)"
