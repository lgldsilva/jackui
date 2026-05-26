package streamer

import "testing"

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		path, want string
	}{
		{"Movie.pt-BR.srt", "pt-BR"},
		{"Movie.pt-br.srt", "pt-BR"},
		{"Movie.por.srt", "pt"},
		{"Movie.portuguese.srt", "pt"},
		{"subs/Portuguese (Brazil)/movie.srt", "pt-BR"},
		{"Movie.eng.srt", "en"},
		{"Movie.english.srt", "en"},
		{"Movie.spa.srt", "es"},
		{"Movie.fre.srt", "fr"},
		{"Movie.ita.srt", "it"},
		{"Movie.ger.srt", "de"},
		{"Movie.jpn.srt", "ja"},
		{"Movie.rus.srt", "ru"},
		{"Movie.chi.srt", "zh"},
		{"Movie.unknown.srt", ""},
	}
	for _, tc := range cases {
		got := detectLanguage(tc.path)
		if got != tc.want {
			t.Errorf("detectLanguage(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSubtitleExtensionsRecognized(t *testing.T) {
	for _, ext := range []string{".srt", ".vtt", ".ass", ".ssa", ".sub"} {
		if _, ok := subtitleExtensions[ext]; !ok {
			t.Errorf("extension %s not recognized", ext)
		}
	}
	for _, ext := range []string{".mp4", ".mkv", ".txt"} {
		if _, ok := subtitleExtensions[ext]; ok {
			t.Errorf("extension %s should NOT be recognized as subtitle", ext)
		}
	}
}
