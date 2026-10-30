package parser

import (
	"encoding/json"
	"testing"
)

func TestDetectAudioFlags(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  AudioFlags
	}{
		// Locked fixtures from the lane spec.
		{"dual pt-br with group", "Movie 2023 1080p BluRay DUAL PT-BR ENG DTS-PiA", AudioFlags{Dual: true, PTBR: true}},
		{"dual audio with group suffix", "Show S01E05 720p WEB-DL DUAL AUDIO x264-SiGLA", AudioFlags{Dual: true}},
		{"multi sets only multi", "Film 2019 1080p MULTi VF2 BluRay x265", AudioFlags{Multi: true}},
		{"latino sets only latino", "Movie 2020 1080p BluRay ENG LATINO DDP5.1", AudioFlags{Latino: true}},
		{"clean release has no flags", "Movie 2015 1080p BluRay x264 YIFY", AudioFlags{}},
		{"dublado", "Filme 2022 1080p WEB-DL DUBLADO DDP5.1", AudioFlags{Dub: true}},
		{"dual with non-pt languages", "Movie 2018 1080p REMUX DUAL RU-EN TrueHD", AudioFlags{Dual: true}},
		{"legendado", "Anime 1000 1080p WEB-DL LEGENDADO x264", AudioFlags{Legendado: true}},
		{"pt.br dot separator", "Movie 2021 2160p DUAL PT.BR DTS-HD", AudioFlags{Dual: true, PTBR: true}},
		{"dot separator combined", "Show S02E03 1080p WEB-DL DUAL PT.BR LEGENDADO x264", AudioFlags{Dual: true, PTBR: true, Legendado: true}},
		// Token-bounded negative cases: flag substrings inside larger tokens.
		{"dual substring in group token", "Movie.2023.1080p.BluRay.x264-DUALBOX", AudioFlags{}},
		{"compound dualaudio is not its own token", "Movie.DUALAUDIO.1080p.BluRay-GROUP", AudioFlags{}},
		{"dub substring in group name", "Movie.2019.1080p.BluRay.x264-REDUB", AudioFlags{}},
		{"dubstep substring", "Movie.Dubstep.Concert.2019.1080p.WEB-DL-GROUP", AudioFlags{}},
		// Trailing release group named "DUB" is a group tag, not a flag.
		{"dub release group is not a flag", "Movie.2022.1080p.WEB-DL.x264-DUB", AudioFlags{}},
		{"dub group with trailing separator is not a flag", "Movie.1080p.x264-DUB.", AudioFlags{}},
		{"dub tag before the group still counts", "Movie.2021.1080p.WEB-DL.DUB-GROUP", AudioFlags{Dub: true}},
		// PTBR forms.
		{"ptbr single token", "Movie 2019 1080p BluRay PTBR AAC2.0-GROUP", AudioFlags{PTBR: true}},
		{"portugues token", "Serie.2020.1080p.PORTUGUES.WEB-DL-GROUP", AudioFlags{PTBR: true}},
		{"portugues accented", "Filme Nacional 2021 1080p PORTUGUÊS DUBLADO WEB-DL", AudioFlags{PTBR: true, Dub: true}},
		{"portugues NFD-decomposed accent", "Movie.2023.PORTUGUE\u0302S.1080p.WEB-DL-GROUP", AudioFlags{PTBR: true}},
		{"pt.br before group", "Movie.2018.720p.WEB-DL.PT.BR-GROUP", AudioFlags{PTBR: true}},
		{"underscore separators", "Movie_2023_1080p_DUAL_PT_BR", AudioFlags{Dual: true, PTBR: true}},
		{"nbsp between tokens", "Movie.2023.1080p.DUAL\u00A0PT-BR.WEB-DL", AudioFlags{Dual: true, PTBR: true}},
		// Legendado and combos.
		{"legendas plural", "Brazilian.Show.2023.1080p.LEGENDAS.PT.BR.WEB-DL", AudioFlags{Legendado: true, PTBR: true}},
		{"dublado e legendado", "Filme.2020.1080p.BluRay.DUBLADO.E.LEGENDADO", AudioFlags{Dub: true, Legendado: true}},
		{"bracketed dual", "Show [DUAL] S01 1080p WEB-DL-GROUP", AudioFlags{Dual: true}},
		// Latino and multi never leak into other flags.
		{"latina feminine form", "Movie.2016.1080p.WEB-DL.LATINA.X264-GROUP", AudioFlags{Latino: true}},
		{"multi tag lowercase", "Film.2014.MULTI.1080p.BluRay.x264-GROUP", AudioFlags{Multi: true}},
		// Case-insensitivity and clean names.
		{"lowercase full match", "movie 2023 1080p web-dl dual pt-br x264", AudioFlags{Dual: true, PTBR: true}},
		{"standalone dub tag", "Show.2024.S01.1080p.DUB.WEB-DL-GROUP", AudioFlags{Dub: true}},
		{"clean series episode", "Show.S03E07.1080p.WEB-DL.DDP5.1.H.264-GROUP", AudioFlags{}},
		{"empty title", "", AudioFlags{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectAudioFlags(tc.title)
			if got != tc.want {
				t.Errorf("DetectAudioFlags(%q) = %+v, want %+v", tc.title, got, tc.want)
			}
		})
	}
}

// The JSON tags are part of the handler/response contract (search results are
// annotated with audioFlags), so they must stay explicit — no omitempty.
func TestAudioFlagsJSONTags(t *testing.T) {
	b, err := json.Marshal(AudioFlags{Dual: true, PTBR: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"dual":true,"dub":false,"ptbr":true,"multi":false,"legendado":false,"latino":false}`
	if got := string(b); got != want {
		t.Errorf("json = %s, want %s", got, want)
	}
}
