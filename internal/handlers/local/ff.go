package local

// ffmpeg/ffprobe invocation constants used by the local media handlers
// (inline ffprobe for local files + subtitle extraction). They live here
// because only this package builds ffmpeg command lines at the handler level;
// torrent transcode goes through internal/transcode.
const (
	ffBinary     = "ffmpeg"
	ffHideBanner = "-hide_banner"
	ffLogLevel   = "-loglevel"
	pipe1        = "pipe:1"
)
