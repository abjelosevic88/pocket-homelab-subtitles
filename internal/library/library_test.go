package library

import "testing"

func TestParseLang(t *testing.T) {
	cases := []struct{ name, video, lang, flags string }{
		{"Show.S01E01.1080p.sr.srt", "Show.S01E01.1080p", "sr", ""},
		{"Show.S01E01.1080p.en.hi.srt", "Show.S01E01.1080p", "en", "hi"},
		{"Movie (2020).pt-br.forced.srt", "Movie (2020)", "pt-br", "forced"},
		{"The Walking Dead S05E12 Remember (1080p x265 Joy).sr.srt", "", "sr", ""},
		{"Something.en.sdh.srt", "", "en", "sdh"},
		{"Plain.srt", "", "", ""},
	}
	for _, c := range cases {
		l, f := ParseLang(c.name, c.video)
		if l != c.lang || f != c.flags {
			t.Errorf("%s: got %q %q, want %q %q", c.name, l, f, c.lang, c.flags)
		}
	}
}

func TestDescribe(t *testing.T) {
	l := New([]string{"/media/shows", "/media/movies"})
	e := l.Describe("/media/shows/The Walking Dead/Season 05/The.Walking.Dead.S05E12.1080p.sr.srt", "/media/shows/The Walking Dead/Season 05/The.Walking.Dead.S05E12.1080p.mkv")
	if e.Title != "The Walking Dead" || e.Label != "S05E12" || e.Kind != "episode" || *e.Season != 5 || *e.Episode != 12 || e.Lang != "sr" || e.Library != "shows" {
		t.Errorf("episode: %+v", e)
	}
	if e.Rel != "shows/The Walking Dead/Season 05/The.Walking.Dead.S05E12.1080p.sr.srt" {
		t.Errorf("rel: %s", e.Rel)
	}
	m := l.Describe("/media/movies/The Pope's Exorcist (2023)/The.Popes.Exorcist.2023.1080p.sr.srt", "/media/movies/The Pope's Exorcist (2023)/The.Popes.Exorcist.2023.1080p.mkv")
	if m.Title != "The Pope's Exorcist (2023)" || m.Kind != "movie" || m.Lang != "sr" {
		t.Errorf("movie: %+v", m)
	}
	loose := l.Describe("/media/movies/Heat.1995.1080p.en.srt", "")
	if loose.Title != "Heat (1995)" || loose.Kind != "movie" || loose.Lang != "en" {
		t.Errorf("loose: %+v", loose)
	}
}

func TestPairDir(t *testing.T) {
	got := pairDir("/m", []string{"A.S01E01.mkv", "A.S01E01.sr.srt", "A.S01E01.en.hi.srt", "A.S01E010.mkv", "A.S01E010.en.srt", "orphan.srt"})
	want := map[string]string{"/m/A.S01E01.sr.srt": "/m/A.S01E01.mkv", "/m/A.S01E01.en.hi.srt": "/m/A.S01E01.mkv", "/m/A.S01E010.en.srt": "/m/A.S01E010.mkv", "/m/orphan.srt": ""}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, p := range got {
		if want[p[1]] != p[0] {
			t.Errorf("%s -> %s, want %s", p[1], p[0], want[p[1]])
		}
	}
}

func TestPrettify(t *testing.T) {
	if got := Prettify("The.Big.Bang.Theory.S07E22.1080p.WEB.sr.srt"); got != "The Big Bang Theory S07E22" {
		t.Error(got)
	}
	if got := Prettify("The.Popes.Exorcist.2023.Hybrid.1080p.BluRay.sr.srt"); got != "The Popes Exorcist (2023)" {
		t.Error(got)
	}
}

// Underscores are word characters: a \b before S01E01 never matched these.
func TestEpisodeNames(t *testing.T) {
	l := New([]string{"/media/tv"})
	cases := []struct{ path, title, label string }{
		{"/media/tv/Fear the Walking Dead/Fear_the_Walking_Dead_S01E01_x265_1080p_BluRay_30nama_30NAMA.sr.srt", "Fear the Walking Dead", "S01E01"},
		{"/media/tv/Fear the Walking Dead/Fear.the.Walking.Dead.S07E10.1080p.en.hi.srt", "Fear the Walking Dead", "S07E10"},
		{"/media/tv/Show/Show - 2x05 - Title.en.srt", "Show", "S02E05"},
		{"/media/tv/Show/show_s3_e12.en.srt", "Show", "S03E12"},
		{"/media/tv/Loose_Show_S02E03_720p.en.srt", "Loose Show", "S02E03"},
	}
	for _, c := range cases {
		e := l.Describe(c.path, "")
		if e.Kind != "episode" || e.Title != c.title || e.Label != c.label {
			t.Errorf("%s: kind=%s title=%q label=%q", c.path, e.Kind, e.Title, e.Label)
		}
	}
	// resolutions and codecs are not episodes
	for _, p := range []string{"/media/tv/Movie/Movie.2019.1920x1080.x265.en.srt", "/media/tv/Movie/Movie_2019_x264.en.srt"} {
		if e := l.Describe(p, ""); e.Kind == "episode" {
			t.Errorf("%s: read as episode %s", p, e.Label)
		}
	}
	if got := Prettify("Fear_the_Walking_Dead_S01E01_x265_1080p.sr.srt"); got != "Fear the Walking Dead S01E01" {
		t.Error(got)
	}
}
