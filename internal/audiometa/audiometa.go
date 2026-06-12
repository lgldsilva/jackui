// Package audiometa reads ID3/MP4/Vorbis/FLAC tags from local audio files via
// the pure-Go github.com/dhowden/tag (no CGO — keeps the Docker build simple).
//
// CRITICAL: the parser reads through the *os.File directly (an io.ReadSeeker).
// dhowden/tag REQUIRES seeking — ID3v1 lives in the last 128 bytes and embedded
// art sits at variable offsets — so a truncating io.LimitReader (no Seek) would
// break it. Passing the *os.File does NOT load the whole file into RAM: the OS
// only pages in the bytes ffmpeg/tag actually Read/Seek over, so even a 24-bit
// hi-res FLAC costs a few KB of header reads, not its full size.
package audiometa

import (
	"os"
	"strings"

	"github.com/dhowden/tag"
)

// Tags is the subset of metadata we surface to the UI. Duration/bitrate are NOT
// here on purpose: dhowden/tag exposes tag fields only; duration comes from
// ffprobe at play time (the HLS VOD path already probes it), so the cache stays
// cheap to populate (header reads, no full decode).
type Tags struct {
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	AlbumArtist string `json:"albumArtist"`
	Genre       string `json:"genre"`
	Year        int    `json:"year"`
	TrackNumber int    `json:"trackNumber"`
	DiscNumber  int    `json:"discNumber"`
	HasCover    bool   `json:"hasCover"`
}

// ReadTags opens absPath and parses its audio tags. A corrupt/unsupported file
// yields an error; callers should treat that as "no tags" rather than fatal —
// dhowden/tag never panics on garbage, it returns tag.ErrNoTagsFound or a parse
// error. The file handle is always closed.
func ReadTags(absPath string) (Tags, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return Tags{}, err
	}
	defer func() { _ = f.Close() }()

	m, err := tag.ReadFrom(f) // io.ReadSeeker — seeks for ID3v1 tail + art offsets
	if err != nil {
		return Tags{}, err
	}
	track, _ := m.Track()
	disc, _ := m.Disc()
	return Tags{
		Title:       strings.TrimSpace(m.Title()),
		Artist:      strings.TrimSpace(m.Artist()),
		Album:       strings.TrimSpace(m.Album()),
		AlbumArtist: strings.TrimSpace(m.AlbumArtist()),
		Genre:       strings.TrimSpace(m.Genre()),
		Year:        m.Year(),
		TrackNumber: track,
		DiscNumber:  disc,
		HasCover:    m.Picture() != nil && len(m.Picture().Data) > 0,
	}, nil
}

// Cover holds an embedded picture's raw bytes plus its MIME type, ready to serve.
type Cover struct {
	Data     []byte
	MIMEType string
}

// ReadCover returns the embedded album art (APIC / METADATA_BLOCK_PICTURE / MP4
// covr). ok=false when the file has no embedded picture. Errors only on I/O or
// parse failure (caller then falls back to TMDB/web art, as the cards already do
// for torrents).
func ReadCover(absPath string) (Cover, bool, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return Cover{}, false, err
	}
	defer func() { _ = f.Close() }()

	m, err := tag.ReadFrom(f)
	if err != nil {
		return Cover{}, false, err
	}
	pic := m.Picture()
	if pic == nil || len(pic.Data) == 0 {
		return Cover{}, false, nil
	}
	mime := pic.MIMEType
	if mime == "" {
		mime = "image/jpeg"
	}
	return Cover{Data: pic.Data, MIMEType: mime}, true, nil
}
