// Package library walks the media folders, pairs every external .srt with the
// video it belongs to and derives a human name for it.
package library

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// VideoExt are the video containers a subtitle can belong to.
var VideoExt = map[string]bool{".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".mov": true, ".ts": true, ".webm": true, ".wmv": true, ".mpg": true}

var (
	// S01E02 / s01.e02 / S01_E02, not glued to a letter or digit before it. No \b:
	// in "Show_Name_S01E02" the underscore is a word character, so \b never fires.
	episodeRE = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])S(\d{1,2})[ ._-]?E(\d{1,3})`)
	// 1x02 as a fallback ("Show - 1x02 - Title"); digits on both sides must stand alone,
	// so 1920x1080 and x265 never match.
	crossRE   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:[^a-z0-9]|$)`)
	movieRE   = regexp.MustCompile(`^(.*?)[. _-]+\(?((?:19|20)\d{2})\)?(?:[. _-]|$)`)
	seasonDir = regexp.MustCompile(`(?i)^(season|staffel|saison|sezona)[ ._-]*\d+$|^s\d{1,2}$|^specials$`)
	// Flags that may follow the language code: x.en.hi.srt, x.sr.forced.srt.
	flagTokens = map[string]bool{"hi": true, "sdh": true, "cc": true, "forced": true, "default": true, "foreign": true}
	langToken  = regexp.MustCompile(`^[a-z]{2,3}(-[a-z]{2,4})?$`)
)

// Library knows the configured folders.
type Library struct {
	Roots []string
}

// New returns a library over the given folders.
func New(roots []string) *Library { return &Library{Roots: roots} }

// RootOf returns the configured folder that contains p ("" if none).
func (l *Library) RootOf(p string) string {
	p = filepath.Clean(p)
	best := ""
	for _, r := range l.Roots {
		if (p == r || strings.HasPrefix(p, r+string(os.PathSeparator))) && len(r) > len(best) {
			best = r
		}
	}
	return best
}

// Contains reports whether p is inside one of the library folders.
func (l *Library) Contains(p string) bool { return l.RootOf(p) != "" }

// Scan walks every folder and returns one entry per external subtitle.
func (l *Library) Scan() ([]store.LibraryEntry, error) {
	var out []store.LibraryEntry
	for _, root := range l.Roots {
		dirs := map[string][]string{}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == root {
					return err
				}
				return nil // unreadable subfolder: skip it, keep scanning
			}
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && p != root {
					return filepath.SkipDir
				}
				return nil
			}
			dirs[filepath.Dir(p)] = append(dirs[filepath.Dir(p)], d.Name())
			return nil
		})
		if err != nil {
			return nil, err
		}
		for dir, files := range dirs {
			for _, e := range pairDir(dir, files) {
				out = append(out, l.Describe(e[1], e[0]))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// pairDir returns [video, subtitle] pairs; a subtitle without a video gets "".
func pairDir(dir string, files []string) [][2]string {
	stems := map[string]string{}
	for _, f := range files {
		if VideoExt[strings.ToLower(filepath.Ext(f))] {
			stems[strings.TrimSuffix(f, filepath.Ext(f))] = f
		}
	}
	var out [][2]string
	for _, f := range files {
		if !strings.EqualFold(filepath.Ext(f), ".srt") {
			continue
		}
		stem := f[:len(f)-4]
		best := ""
		for s := range stems {
			if (stem == s || strings.HasPrefix(stem, s+".")) && len(s) > len(best) {
				best = s
			}
		}
		video := ""
		if best != "" {
			video = filepath.Join(dir, stems[best])
		}
		out = append(out, [2]string{video, filepath.Join(dir, f)})
	}
	return out
}

// FindVideo finds the video a subtitle belongs to, by file name.
func FindVideo(subtitle string) string {
	dir := filepath.Dir(subtitle)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var files []string
	for _, e := range ents {
		files = append(files, e.Name())
	}
	for _, p := range pairDir(dir, files) {
		if p[1] == subtitle {
			return p[0]
		}
	}
	return ""
}

// episodeIn finds the season and episode in a file name; prefix is the text before it.
func episodeIn(base string) (season, episode int, prefix string, ok bool) {
	for _, re := range []*regexp.Regexp{episodeRE, crossRE} {
		if m := re.FindStringSubmatchIndex(base); m != nil {
			season, _ = strconv.Atoi(base[m[2]:m[3]])
			episode, _ = strconv.Atoi(base[m[4]:m[5]])
			return season, episode, base[:m[0]], true
		}
	}
	return 0, 0, "", false
}

// Describe derives names and language for one subtitle.
func (l *Library) Describe(subtitle, video string) store.LibraryEntry {
	e := store.LibraryEntry{Path: subtitle, VideoPath: video, Kind: "other"}
	root := l.RootOf(subtitle)
	if root != "" {
		e.Library = filepath.Base(root)
		if rel, err := filepath.Rel(filepath.Dir(root), subtitle); err == nil {
			e.Rel = rel
		}
	} else {
		e.Rel = filepath.Base(subtitle)
	}
	name := filepath.Base(subtitle)
	videoStem := ""
	if video != "" {
		videoStem = strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	}
	e.Lang, e.Flags = ParseLang(name, videoStem)

	// The show/movie is the first folder below the library root; files lying
	// directly in the root fall back to what the file name says.
	title := ""
	if root != "" {
		if rel, err := filepath.Rel(root, subtitle); err == nil {
			parts := strings.Split(rel, string(os.PathSeparator))
			if len(parts) > 1 && !seasonDir.MatchString(parts[0]) {
				title = parts[0]
			}
		}
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if videoStem != "" {
		base = videoStem
	}
	if s, ep, prefix, ok := episodeIn(base); ok {
		e.Kind, e.Season, e.Episode = "episode", &s, &ep
		e.Label = "S" + pad2(s) + "E" + pad2(ep)
		if title == "" {
			title = cleanName(prefix)
		}
	} else if mm := movieRE.FindStringSubmatch(base); mm != nil {
		e.Kind = "movie"
		if title == "" {
			title = cleanName(mm[1]) + " (" + mm[2] + ")"
		}
		e.Label = title
	} else {
		if title == "" {
			title = cleanName(base)
		}
		e.Label = title
		if e.Library != "" && strings.Contains(strings.ToLower(e.Library), "movie") {
			e.Kind = "movie"
		}
	}
	e.Title = title
	return e
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func cleanName(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = strings.Trim(s, " -")
	return strings.Join(strings.Fields(s), " ")
}

// ParseLang returns the language and flags of a subtitle file name, from the
// tokens between the video's name and ".srt" when the video is known
// ("Show.S01E01.sr.hi.srt" → "sr", "hi"), else from the trailing tokens.
func ParseLang(name, videoStem string) (lang, flags string) {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	var toks []string
	if videoStem != "" && strings.HasPrefix(stem, videoStem+".") {
		toks = strings.Split(stem[len(videoStem)+1:], ".")
	} else {
		// no video to anchor on: take the trailing flags and one language token
		all := strings.Split(stem, ".")
		start := len(all)
		for i := len(all) - 1; i > 0; i-- {
			tok := strings.ToLower(all[i])
			if flagTokens[tok] {
				start = i
				continue
			}
			if langToken.MatchString(tok) {
				start = i
			}
			break
		}
		toks = all[start:]
	}
	var fl []string
	for _, t := range toks {
		t = strings.ToLower(t)
		switch {
		case flagTokens[t]:
			fl = append(fl, t)
		case lang == "" && langToken.MatchString(t):
			lang = t
		}
	}
	return lang, strings.Join(fl, ".")
}

// Prettify turns a release file name into "Show S01E02" / "Movie (2019)".
func Prettify(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if s, ep, prefix, ok := episodeIn(base); ok {
		return cleanName(prefix) + " S" + pad2(s) + "E" + pad2(ep)
	}
	if mm := movieRE.FindStringSubmatch(base); mm != nil {
		return cleanName(mm[1]) + " (" + mm[2] + ")"
	}
	return cleanName(base)
}
