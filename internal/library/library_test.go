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
