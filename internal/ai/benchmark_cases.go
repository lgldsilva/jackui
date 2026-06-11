package ai

// OriginDefault tags a BenchmarkCase that ships with JackUI (vs a user-added
// one, whose Origin is empty). Informational only — scoring ignores it.
const OriginDefault = "default"

// DefaultBenchmarkCases seeds a fresh store. The set is deliberately broad —
// every category below exists because the tiny original 7-case set made models
// look better than they were: it had no season packs, no year-in-title traps,
// no BR/dubbed releases, no site-tag prefixes, no live events, no adult-scene
// naming — exactly the inputs weak models botch in production. Expects use the
// canonical label parsed by parseExpect:
//
//	Movie:        "Inception - 2010"       (Título - Ano; year informational, not scored)
//	TV episode:   "Breaking Bad - S03E07"  (title 60% + season/episode 40%)
//	Episode only: "Frieren - E01"          (anime absolute numbering)
//	Season pack:  "The Wire - S04"         (title 60% + season 40%)
//	Plain title:  "Metallica"              (title only)
//
// GUARD: none of these raws may appear as a few-shot example inside
// renameSystem/identifySystem — that would let models copy the answer from the
// prompt and inflate the benchmark (enforced by TestDefaultCasesNotInPrompts).
var DefaultBenchmarkCases = markDefault([]BenchmarkCase{
	// ── Movies: straightforward scene names ─────────────────────────────────
	{Raw: "Inception.2010.1080p.BluRay.x264-SPARKS", Expect: "Inception - 2010"},
	{Raw: "The.Matrix.1999.2160p.UHD.BluRay.x265-TERMINAL", Expect: "The Matrix - 1999"},
	{Raw: "Dune.Part.Two.2024.1080p.WEB-DL.DDP5.1.Atmos.H.264-FLUX", Expect: "Dune Part Two - 2024"},
	{Raw: "Oppenheimer.2023.1080p.WEBRip.x264-RARBG", Expect: "Oppenheimer - 2023"},
	{Raw: "The.Shawshank.Redemption.1994.720p.BrRip.x264.YIFY", Expect: "The Shawshank Redemption - 1994"},
	{Raw: "Everything.Everywhere.All.at.Once.2022.1080p.AMZN.WEB-DL.DDP5.1.H.264-CMRG", Expect: "Everything Everywhere All at Once - 2022"},

	// ── Separators: underscores, spaces+brackets, hyphens ───────────────────
	{Raw: "Spider-Man_Across_the_Spider-Verse_2023_1080p_WEBRip_x265", Expect: "Spider-Man Across the Spider-Verse - 2023"},
	{Raw: "john wick chapter 4 (2023) [1080p] [BluRay] [5.1] [YTS.MX]", Expect: "John Wick Chapter 4 - 2023"},
	{Raw: "The-Grand-Budapest-Hotel-2014-1080p-BluRay-x264", Expect: "The Grand Budapest Hotel - 2014"},

	// ── Traps: a year (or "2000"/"2012") that is PART of the title ──────────
	{Raw: "1917.2019.1080p.BluRay.x264-AAA", Expect: "1917 - 2019"},
	{Raw: "Blade.Runner.2049.2017.2160p.UHD.BluRay.x265-IAMABLE", Expect: "Blade Runner 2049 - 2017"},
	{Raw: "Wonder.Woman.1984.2020.1080p.HMAX.WEB-DL.DDP5.1.Atmos.x264-EVO", Expect: "Wonder Woman 1984 - 2020"},
	{Raw: "2001.A.Space.Odyssey.1968.2160p.UHD.BluRay.x265-DEPTH", Expect: "2001 A Space Odyssey - 1968"},
	{Raw: "2012.2009.1080p.BluRay.x264-METiS", Expect: "2012 - 2009"},
	{Raw: "Death.Race.2000.1975.1080p.BluRay.x264-USURY", Expect: "Death Race 2000 - 1975"},

	// ── Remux / 4K / HDR / DV / 10bit ───────────────────────────────────────
	{Raw: "Interstellar.2014.2160p.UHD.BluRay.REMUX.HDR.HEVC.Atmos-EPSiLON", Expect: "Interstellar - 2014"},
	{Raw: "Mad.Max.Fury.Road.2015.2160p.BluRay.x265.10bit.HDR10Plus.DTS-HD.MA.7.1-SWTYBLZ", Expect: "Mad Max Fury Road - 2015"},
	{Raw: "Avatar.The.Way.of.Water.2022.2160p.WEB-DL.DV.HDR.HEVC.DDP5.1.Atmos-FLUX", Expect: "Avatar The Way of Water - 2022"},

	// ── Editions: REPACK / PROPER / EXTENDED / Special Edition ──────────────
	{Raw: "Aliens.1986.Special.Edition.1080p.BluRay.x264-GECKOS", Expect: "Aliens - 1986"},
	{Raw: "The.Lord.of.the.Rings.The.Two.Towers.2002.EXTENDED.1080p.BluRay.x264-SiNNERS", Expect: "The Lord of the Rings The Two Towers - 2002"},
	{Raw: "Apocalypse.Now.1979.Final.Cut.PROPER.REPACK.1080p.BluRay.x264-CiNEFiLE", Expect: "Apocalypse Now - 1979"},

	// ── TV episodes: SxxEyy, episode titles, lowercase, NxMM notation ───────
	{Raw: "Breaking.Bad.S03E07.720p.HDTV.x264-CTU", Expect: "Breaking Bad - S03E07"},
	{Raw: "Game.of.Thrones.S01E09.Baelor.1080p.BluRay.x264-DEMAND", Expect: "Game of Thrones - S01E09"},
	{Raw: "The.Last.of.Us.S02E03.1080p.WEB.H264-SuccessfulCrab", Expect: "The Last of Us - S02E03"},
	{Raw: "severance.s02e05.1080p.web.h264-successfulcrab", Expect: "Severance - S02E05"},
	{Raw: "The.Bear.S03E01.Tomorrow.1080p.HULU.WEB-DL.DDP5.1.H.264-FLUX", Expect: "The Bear - S03E01"},
	{Raw: "Stranger.Things.4x09.The.Piggyback.2160p.NF.WEB-DL", Expect: "Stranger Things - S04E09"},
	{Raw: "The.Office.US.S05E14.Stress.Relief.720p.HDTV.x264", Expect: "The Office US - S05E14"},

	// ── Season packs: SxxCOMPLETE / "Season N" — title + season, no episode ─
	{Raw: "The.Wire.S04.COMPLETE.1080p.BluRay.x264-AVCHD", Expect: "The Wire - S04"},
	{Raw: "Chernobyl.S01.COMPLETE.2160p.UHD.BluRay.x265-AJP69", Expect: "Chernobyl - S01"},
	{Raw: "Dark.S03.German.1080p.NF.WEB-DL.DD5.1.x264-TEPES", Expect: "Dark - S03"},
	{Raw: "Friends.Season.2.Complete.720p.BluRay.x264-PSYCHD", Expect: "Friends - S02"},

	// ── Anime: [Group] prefix, absolute numbering, romanized titles ─────────
	{Raw: "[SubsPlease] Sousou no Frieren - 05 (1080p) [F02B9CEE].mkv", Expect: "Sousou no Frieren - E05"},
	{Raw: "[Erai-raws] Frieren - 01 [1080p][Multiple Subtitle]", Expect: "Frieren - E01"},
	{Raw: "[Judas] Jujutsu Kaisen S2 - 23 [1080p][HEVC x265 10bit][Multi-Subs]", Expect: "Jujutsu Kaisen - S02E23"},
	{Raw: "[HorribleSubs] One Punch Man - 12 [720p].mkv", Expect: "One Punch Man - E12"},
	{Raw: "Kimetsu.no.Yaiba.S04E08.1080p.CR.WEB-DL.AAC2.0.H.264-VARYG", Expect: "Kimetsu no Yaiba - S04E08"},

	// ── BR: DUBLADO / NACIONAL / DUAL — title must stay in Portuguese ───────
	{Raw: "O.Auto.da.Compadecida.2000.DUBLADO.1080p", Expect: "O Auto da Compadecida - 2000"},
	{Raw: "Cidade.de.Deus.2002.NACIONAL.1080p.BluRay.x264", Expect: "Cidade de Deus - 2002"},
	{Raw: "Ainda.Estou.Aqui.2024.1080p.AMZN.WEB-DL.DDP5.1.DUAL-DUBLADO", Expect: "Ainda Estou Aqui - 2024"},
	{Raw: "Tropa.de.Elite.2.2010.NACIONAL.720p.BluRay.x264-CiNEFiLHOS", Expect: "Tropa de Elite 2 - 2010"},
	{Raw: "Divertida.Mente.2.2024.DUBLADO.DUAL.AUDIO.1080p.WEB-DL", Expect: "Divertida Mente 2 - 2024"},
	{Raw: "Round.6.S03E01.DUAL.1080p.NF.WEB-DL.DDP5.1.Atmos-NTb", Expect: "Round 6 - S03E01"},

	// ── Accented / non-English titles (scoring is accent-insensitive) ───────
	{Raw: "Amelie.2001.FRENCH.1080p.BluRay.x264-LOST", Expect: "Amélie - 2001"},
	{Raw: "La.Casa.de.Papel.S02E03.SPANISH.1080p.NF.WEB-DL.DD5.1.x264-PECULATE", Expect: "La Casa de Papel - S02E03"},
	{Raw: "Sen.to.Chihiro.no.Kamikakushi.2001.JAPANESE.1080p.BluRay.x264-MOOVEE", Expect: "Sen to Chihiro no Kamikakushi - 2001"},
	{Raw: "Oldboy.2003.KOREAN.REMASTERED.1080p.BluRay.x264-GiMCHi", Expect: "Oldboy - 2003"},

	// ── Documentaries ────────────────────────────────────────────────────────
	{Raw: "Planet.Earth.II.S01E01.Islands.2160p.UHD.BluRay.x265-WhiteRhino", Expect: "Planet Earth II - S01E01"},
	{Raw: "Free.Solo.2018.1080p.BluRay.x264-ROVERS", Expect: "Free Solo - 2018"},
	{Raw: "Senna.2010.DOCUMENTARY.1080p.BluRay.x264-HAGGiS", Expect: "Senna - 2010"},

	// ── Live events: UFC / F1 / WWE — event name + number is the title ──────
	{Raw: "UFC.300.Pereira.vs.Hill.PPV.2024.04.13.1080p.WEB.h264-VERUM", Expect: "UFC 300 Pereira vs Hill - 2024"},
	{Raw: "Formula1.2024.Round22.Las.Vegas.Grand.Prix.Race.SkyF1HD.1080p", Expect: "Formula 1 Las Vegas Grand Prix Race - 2024"},
	{Raw: "WWE.WrestleMania.40.Night.1.PPV.2024.1080p.PCOK.WEB-DL.H264", Expect: "WWE WrestleMania 40 Night 1 - 2024"},

	// ── Music: album → "Artist - Album"; discography → artist only ──────────
	{Raw: "Pink.Floyd.The.Dark.Side.Of.The.Moon.1973.FLAC.24bit-96kHz", Expect: "Pink Floyd - The Dark Side of the Moon - 1973"},
	{Raw: "Metallica - Discography (1983-2016) [320kbps]", Expect: "Metallica"},

	// ── Adult: studio.YY.MM.DD.performer.scene — must keep performer+scene ──
	{Raw: "Brazzers.24.03.15.Angela.White.Wet.And.Wild.XXX.1080p.MP4-XXX", Expect: "Brazzers - Angela White - Wet And Wild"},
	{Raw: "SexArt.23.11.02.Lika.Star.Morning.Coffee.XXX.2160p.MP4-WRB", Expect: "SexArt - Lika Star - Morning Coffee"},
	{Raw: "PlayboyPlus.22.07.19.Gloria.Sol.Natural.Beauty.XXX.1080p", Expect: "PlayboyPlus - Gloria Sol - Natural Beauty"},

	// ── Site-tag prefixes ────────────────────────────────────────────────────
	{Raw: "www.Torrenting.com - The.Batman.2022.1080p.WEBRip.x264-YIFY", Expect: "The Batman - 2022"},
	{Raw: "www.UIndex.org    -    Gladiator.II.2024.1080p.WEB-DL.H264-ETHEL", Expect: "Gladiator II - 2024"},
	{Raw: "[ Torrent911.my ] Le.Comte.de.Monte-Cristo.2024.FRENCH.1080p.WEB.H264-FW", Expect: "Le Comte de Monte-Cristo - 2024"},

	// ── Short, ambiguous titles — easy to over-strip ─────────────────────────
	{Raw: "Up.2009.1080p.BluRay.x264-CBGB", Expect: "Up - 2009"},
	{Raw: "It.2017.1080p.BluRay.x264-SPARKS", Expect: "It - 2017"},
	{Raw: "Her.2013.720p.BluRay.x264-GECKOS", Expect: "Her - 2013"},
	{Raw: "Us.2019.1080p.WEB-DL.DD5.1.H264-CMRG", Expect: "Us - 2019"},
})

// DefaultScheduleCases seed the SCHEDULE task — the natural-language → schedule
// parse used by the watchlist. They exist because the chain used to be ranked on
// rename ALONE, so a model that's terrible at schedules could sit at the top and
// turn "Toda segunda-feira às 07h00" into a daily 07:00 (bug A). Expect uses the
// compact label parsed by parseScheduleExpect: "weekly:<weekday>:<HH>:<MM>",
// "daily:<HH>:<MM>", "interval:<minutes>" (weekday 0=Sun … 6=Sat). The weekday is
// scored heavily, so a wrong day (or a daily-instead-of-weekly) genuinely hurts.
//
// GUARD: none of these raws may appear verbatim in scheduleSystem's few-shot set
// (would let a model copy the answer) — enforced by TestScheduleCasesNotInPrompt.
var DefaultScheduleCases = markDefaultTask([]BenchmarkCase{
	// Weekly — the long hyphenated PT form + HHhMM clock (the exact bug shape), in
	// phrasings DISTINCT from scheduleSystem's few-shot so the score is honest.
	{Raw: "checar toda segunda-feira às 06h45", Expect: "weekly:1:6:45"},
	{Raw: "rodar nas terças-feiras 19h", Expect: "weekly:2:19:0"},
	{Raw: "às quartas-feiras de manhã", Expect: "weekly:3:8:0"},
	{Raw: "atualizar quinta-feira às 23h30", Expect: "weekly:4:23:30"},
	{Raw: "todo sábado às 10h", Expect: "weekly:6:10:0"},
	{Raw: "aos domingos de tarde", Expect: "weekly:0:14:0"},
	{Raw: "check it every Friday at 8pm", Expect: "weekly:5:20:0"},
	// Daily — no weekday named (the contrast that must NOT become weekly).
	{Raw: "uma vez por dia às 05h00", Expect: "daily:5:0"},
	{Raw: "diariamente às 23:15", Expect: "daily:23:15"},
	{Raw: "check once a day at 6pm", Expect: "daily:18:0"},
	// Interval — period in minutes.
	{Raw: "a cada 2 horas", Expect: "interval:120"},
	{Raw: "três vezes por dia", Expect: "interval:480"},
	{Raw: "every 30 minutes", Expect: "interval:30"},
}, TaskSchedule)

// DefaultIdentifyCases seed the IDENTIFY task — the title-ONLY extraction used by
// categoryFromAI and the art fail-safe chain. Scored on the title alone (no
// season/episode), so Expect is a bare title (the "- YYYY" tail is informational).
// Distinct raws from the rename set so the two tasks measure different inputs.
//
// GUARD: not in identifySystem's prompt — enforced by TestIdentifyCasesNotInPrompt.
var DefaultIdentifyCases = markDefaultTask([]BenchmarkCase{
	{Raw: "Sicario.2015.1080p.BluRay.x264-SPARKS", Expect: "Sicario"},
	{Raw: "Arrival.2016.2160p.UHD.BluRay.x265-TERMINAL", Expect: "Arrival"},
	{Raw: "The.Witcher.S02E01.1080p.NF.WEB-DL.DDP5.1.x264-NTb", Expect: "The Witcher"},
	{Raw: "Parasite.2019.KOREAN.1080p.BluRay.x264-REGRET", Expect: "Parasite"},
	{Raw: "[SubsPlease] Chainsaw Man - 04 (1080p) [9A8B7C6D].mkv", Expect: "Chainsaw Man"},
	{Raw: "Cidade.Baixa.2005.NACIONAL.1080p.WEB-DL", Expect: "Cidade Baixa"},
}, TaskIdentify)

// AllDefaultBenchmarkCases is the full multi-task seed: rename + schedule + identify.
// The store seeds this on first use so the benchmark covers every AI task out of
// the box. DefaultBenchmarkCases (rename only) is kept for the prompt-leak guards
// and legacy-seed detection.
func AllDefaultBenchmarkCases() []BenchmarkCase {
	out := make([]BenchmarkCase, 0, len(DefaultBenchmarkCases)+len(DefaultScheduleCases)+len(DefaultIdentifyCases))
	out = append(out, DefaultBenchmarkCases...)
	out = append(out, DefaultScheduleCases...)
	out = append(out, DefaultIdentifyCases...)
	return out
}

// markDefaultTask stamps both the default origin and the given task on each case.
func markDefaultTask(cases []BenchmarkCase, task string) []BenchmarkCase {
	for i := range cases {
		cases[i].Origin = OriginDefault
		cases[i].Task = task
	}
	return cases
}

// markDefault stamps the built-in origin on every seeded case so the UI (and a
// future "restore defaults") can tell shipped cases from user-added ones.
func markDefault(cases []BenchmarkCase) []BenchmarkCase {
	for i := range cases {
		cases[i].Origin = OriginDefault
	}
	return cases
}

// legacySeedCases is the original 7-case default set shipped before the broad
// dataset above. Kept ONLY so Cases() can recognize a store still holding that
// untouched seed and upgrade it to the new defaults — an EDITED set (anything
// that differs) is the user's and is never replaced.
var legacySeedCases = []BenchmarkCase{
	{Raw: "Inception.2010.1080p.BluRay.x264-SPARKS", Expect: "Inception - 2010"},
	{Raw: "The.Matrix.1999.2160p.UHD.BluRay.x265-TERMINAL", Expect: "The Matrix - 1999"},
	{Raw: "Breaking.Bad.S03E07.720p.HDTV.x264-CTU", Expect: "Breaking Bad - S03E07"},
	{Raw: "Game.of.Thrones.S01E09.Baelor.1080p.BluRay.x264-DEMAND", Expect: "Game of Thrones - S01E09"},
	{Raw: "Dune.Part.Two.2024.1080p.WEB-DL.DDP5.1.Atmos.H.264-FLUX", Expect: "Dune Part Two - 2024"},
	{Raw: "[Erai-raws] Frieren - 01 [1080p][Multiple Subtitle]", Expect: "Frieren - E01"},
	{Raw: "O.Auto.da.Compadecida.2000.DUBLADO.1080p", Expect: "O Auto da Compadecida - 2000"},
}

// isLegacySeed reports whether a stored case set is exactly the pre-expansion
// default seed (raw+expect, in order; origin ignored — old builds never wrote
// one). Such a set was never touched by the user, so upgrading it is safe.
func isLegacySeed(cases []BenchmarkCase) bool {
	if len(cases) != len(legacySeedCases) {
		return false
	}
	for i, c := range cases {
		if c.Raw != legacySeedCases[i].Raw || c.Expect != legacySeedCases[i].Expect {
			return false
		}
	}
	return true
}

// isRenameOnlySeed reports whether a stored set is exactly the untouched broad
// rename-only default (the set shipped BEFORE schedule/identify tasks existed):
// the same raws+expects as DefaultBenchmarkCases, in order, none carrying a task.
// Such a set was never edited by the user, so upgrading it to the full multi-task
// seed is safe. ANY difference (a user edit, or a set that already has tasks) is
// left alone.
func isRenameOnlySeed(cases []BenchmarkCase) bool {
	if len(cases) != len(DefaultBenchmarkCases) {
		return false
	}
	for i, c := range cases {
		if c.Raw != DefaultBenchmarkCases[i].Raw || c.Expect != DefaultBenchmarkCases[i].Expect {
			return false
		}
		if normalizeTask(c.Task) != TaskRename {
			return false // already has a non-rename task → not the old rename-only seed
		}
	}
	return true
}
