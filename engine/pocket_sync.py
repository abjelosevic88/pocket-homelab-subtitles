#!/usr/bin/env python3
"""Pocket Subtitles sync engine.

Measures a subtitle against its video's audio (and, when one proves trustworthy,
an embedded text track) and rewrites the timing only when it is sure.

Policy: measure first, act only when sure, never make a file worse.
  1. Prefer an embedded English text track that agrees with the audio (whole
     file AND its last 30%), and align to it with alass: that follows any number
     of cut differences.
  2. Otherwise whole-file audio alignment (narrow +-15s search, widened to +-40s
     if pinned at the bound). Black-frame act segmentation then handles stepped
     cuts; every segment offset is anchored to the whole-file answer by outlier
     passes, and a polish pass catches short displaced blocks.
  3. If nothing can be measured, the file is left untouched.
  4. A content-fit gate rejects subtitles whose text does not belong to this
     video (wrong episode), and a text gate rejects machine translations of
     fresh downloads (sr/hr/bs heuristics).

The engine never deletes files and never talks to the network: the Go server
decides what happens to rejected files, refreshes Jellyfin and records history.

    pocket_sync.py sync   VIDEO SUBTITLE [options]   -> one JSON line on stdout
    pocket_sync.py verify VIDEO SUBTITLE [options]   -> one JSON line on stdout
    pocket_sync.py doctor                            -> versions of the toolchain

Progress goes to stderr as "@@stage <text>" lines.

The first original of every modified subtitle is kept forever at --backup-path.
Measurements (speech references, act boundaries, embedded tracks) are cached
per video (file name + size) in --cache-dir.
"""
import argparse, json, os, re, shutil, subprocess, sys, tempfile, time

FFSUBSYNC_LIBS = os.environ.get("FFSUBSYNC_LIBS", "")   # extra import path (e.g. Bazarr's vendored libs)
CACHE_DIR = os.environ.get("POCKET_CACHE_DIR", "/data/cache")
BACKUP_PATH = None             # where the first original of THIS subtitle goes (set by main)
ALASS = os.environ.get("ALASS_BIN", "alass-cli")
TEXT_SUB_CODECS = {"subrip", "ass", "ssa", "mov_text"}
REF_MAX_AUDIO_OFFSET = 1.5     # embedded track must agree with the audio this closely,
                               # or it is from the wrong cut (seen on x265 Joy re-encodes)
                               # and must not be used as a reference
MIN_CUES_PER_SEG = 12          # segments smaller than this merge into the previous one
MIN_CUES = 20                  # fewer cues than this = not a real subtitle, skip
MIN_BLACK = 0.7                # seconds of black to count as an act break
BOUNDARY_MERGE = 30.0          # blacks closer together than this are one boundary
EDGE_GUARD = 60.0              # ignore blacks this close to start/end
CAP_NARROW = "15"              # conservative search (strings: go straight into argv)
MAX_OFFSET = "40"              # widest search. NOT wider: at >=90s the correlation
                               # surface grows spurious peaks that outscore the truth
                               # (S10E18 reads 0.00s at +-30 but 89.92s at +-90).
CAP_EPS = 0.5                  # an offset this close to a search bound is pinned => invalid
MAX_DEVIATION = 30             # max a segment may stray from the whole-file offset
NOOP_THRESHOLD = 0.5           # everything within this of zero = already in sync
# Audio alignment puts a cue's start on the speech onset. Watching that back,
# subtitles can read as appearing slightly early — most published subtitles carry
# a lead-in. LEAD_OUT moves every cue this much AFTER the voice. Applied last,
# after alignment and polish, so it never distorts a measurement. 0 = pure audio
# timing. Set with --lead.
LEAD_OUT = 0.0
LEAD_TOLERANCE = 0.15          # already within this of the target lead = leave alone
REF_RESIDUAL = False           # OFF by default: on dialogue-sparse acts it pushed
                               # cues +0.45s AWAY from a pro track (noise); it only
                               # helps where the track itself drifts
REF_RESIDUAL_CAP = "2"         # post-alass audio re-measure: the file is already
                               # aligned, so only a tight search is meaningful
REF_RESIDUAL_MIN = 0.2         # below this nobody notices; do not churn files
REF_RESIDUAL_MAX = 1.5         # = REF_MAX_AUDIO_OFFSET; anything larger means the
                               # reference should have failed its audio check
REF_RESIDUAL_MIN_GAIN = 0.005  # speech coverage must actually improve
REFCHECK_SCHEMA = "tailcheck-v2"   # bump to invalidate cached embedded-track verdicts
CENTRE_REF_TAG = "centre-v1"   # bump to rebuild centre-channel speech references
MIN_SPEECH_DENSITY = 0.05      # below this the speech reference is unusable —
                               # refuse to align rather than measure noise
CENTRE_MIN_EDGE = 0.05         # centre must beat the mix by this to be trusted
CENTRE_TRIGGER = 0.60          # only look for a better reference below this —
                               # above it the mix already tracks the dialogue
CENTRE_FILTER = ("pan=mono|c0=FC,dynaudnorm=f=200:g=15:p=0.9,"
                 "highpass=f=90,lowpass=f=7500")
CENTRE_REF = False             # --centre-ref: try the 5.1 centre channel as reference
POLISH_APPLY = False           # --polish: apply (not just record) the polish pass
POLISH_GROUP = 9               # cues weighed together when re-measuring one cue
POLISH_MAX = 5.0               # widest residual the polish pass will consider.
                               # Bigger displacements belong to the segment/alass
                               # logic; allowing 8 here let a false -7.6s through
                               # on a file that was already correct.
POLISH_CORROBORATE = 10        # a lone block must be at least this many cues,
                               # otherwise another block must agree with it —
                               # real cut drift repeats, false peaks are isolated
POLISH_MIN_SHIFT = 0.6         # below this nobody notices; do not churn files
POLISH_MIN_GAIN = 0.10         # coverage must improve this much to act
POLISH_MIN_MARGIN = 0.05       # ...and beat its nearest rival by this much
POLISH_MIN_COVERAGE = 0.35     # ...and land on real speech (blind scenes fail here)
POLISH_MIN_RUN = 3             # consecutive cues that must agree before applying

# Content-fit gate. Alignment can only shift cues in time — it cannot tell
# whether the TEXT belongs to this video at all. A subtitle for the wrong
# episode sails through every offset check (alass reported max_shift 0.31s on
# an S04E01 subtitle mislabeled as S04E02), so after alignment the final cues
# must actually sit on the detected speech. Absolute floor: wrong-episode
# content measured 0.39 while the worst legitimate subtitle in a 30-file
# library sample measured 0.51. Relative check: noisy audio drags a correct
# subtitle and the video's own embedded track down TOGETHER (ratios 0.9-1.0
# measured, even where both fits were 0.51-0.53), while wrong content drags
# only the subtitle (ratio 0.39) — so the ratio separates far better than the
# floor wherever a trusted embedded track exists.
FIT_REACH = 0.6                # the file is already aligned; allow lead + slop only
FIT_STEP = 0.2
FIT_FLOOR = 0.45               # below this = wrong content (unless the video's own
                               # trusted track ALSO scores under FIT_MIN_REF - then
                               # the VAD is the problem and only FIT_RATIO applies)
FIT_RATIO = 0.60               # must reach this fraction of the embedded track's fit
FIT_MIN_REF = 0.50             # a ref below this marks the speech map unreliable

TS = re.compile(r"(\d\d):(\d\d):(\d\d)[,.](\d{3}) --> (\d\d):(\d\d):(\d\d)[,.](\d{3})")


def stage(text):
    """Progress line for the server ("now syncing" shows it)."""
    try:
        sys.stderr.write(f"@@stage {text}\n")
        sys.stderr.flush()
    except OSError:
        pass


def run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


def _ffs_prelude():
    """Python prelude for an ffsubsync child process."""
    code = "import sys\n"
    if FFSUBSYNC_LIBS:
        code += f"sys.path.insert(0,{FFSUBSYNC_LIBS!r})\n"
    return code


def ffprobe_duration(video):
    r = run(["ffprobe", "-v", "error", "-show_entries", "format=duration",
             "-of", "csv=p=0", video])
    return float(r.stdout.strip())


def detect_boundaries(video, duration):
    """Black-frame act boundaries (midpoints), merged and edge-guarded.
    Cached per video (basename+size) alongside the speech cache."""
    os.makedirs(CACHE_DIR, exist_ok=True)
    st = os.stat(video)
    cached = os.path.join(CACHE_DIR,
                          f"{os.path.basename(video)}.{st.st_size}.bounds.json")
    if os.path.exists(cached):
        return json.load(open(cached))
    r = run(["ffmpeg", "-hide_banner", "-nostats", "-i", video,
             "-vf", f"blackdetect=d={MIN_BLACK}:pix_th=0.10", "-an", "-f", "null", "-"])
    blacks = [(float(a), float(b)) for a, b in
              re.findall(r"black_start:([\d.]+) black_end:([\d.]+)", r.stderr)]
    mids = [(s + e) / 2 for s, e in blacks if EDGE_GUARD < s < duration - EDGE_GUARD]
    merged = []
    for m in sorted(mids):
        if merged and m - merged[-1] < BOUNDARY_MERGE:
            merged[-1] = (merged[-1] + m) / 2
        else:
            merged.append(m)
    json.dump(merged, open(cached, "w"))
    return merged


def parse_srt(path):
    raw = open(path, encoding="utf-8", errors="replace").read()
    cues = []
    for block in re.split(r"\n\s*\n", raw.strip()):
        m = TS.search(block)
        if not m:
            continue
        g = list(map(int, m.groups()))
        start = g[0] * 3600 + g[1] * 60 + g[2] + g[3] / 1000
        end = g[4] * 3600 + g[5] * 60 + g[6] + g[7] / 1000
        body = re.sub(r"^\d+\s*\n", "", block.strip())
        text = "\n".join(l for l in body.splitlines() if not TS.search(l))
        cues.append((start, end, text))
    cues.sort()
    return cues


MT_CREDIT = re.compile(r"(preveo|prevela|prevod|prijevod|preveden|obrad[aio]"
                       r"|titlovi\.com|podnapisi|uredi[ol]|prilagodi[ol])",
                       re.I)


CYR = {'а':'a','б':'b','в':'v','г':'g','д':'d','ђ':'đ','е':'e','ж':'ž','з':'z','и':'i',
       'ј':'j','к':'k','л':'l','љ':'lj','м':'m','н':'n','њ':'nj','о':'o','п':'p','р':'r',
       'с':'s','т':'t','ћ':'ć','у':'u','ф':'f','х':'h','ц':'c','ч':'č','џ':'dž','ш':'š'}
CYR_UP = {k.upper(): v.capitalize() for k, v in CYR.items()}


def cyr2lat(text):
    """Serbian Cyrillic -> Latin, lossless 1:1 (Lj/NJ casing by neighbours)."""
    out = []
    for i, ch in enumerate(text):
        if ch in CYR:
            out.append(CYR[ch])
        elif ch in CYR_UP:
            v = CYR_UP[ch]
            if len(v) > 1:
                nxt = text[i + 1] if i + 1 < len(text) else ""
                prv = text[i - 1] if i else ""
                if (nxt and (nxt in CYR_UP or nxt.isupper())) or \
                        (prv and (prv in CYR_UP or prv.isupper())
                         and not (nxt in CYR or nxt.islower())):
                    v = v.upper()
            out.append(v)
        else:
            out.append(ch)
    return "".join(out)


def latinize_if_cyrillic(path):
    """Serbian subtitle packs often ship both scripts and Bazarr picks one at
    random (one season came back 10 of 16 files Cyrillic). Rewrites the file in
    place when the library convention is Latin; returns True if it changed.
    Keeps the first original at the backup path."""
    raw = open(path, encoding="utf-8-sig", errors="replace").read()
    if not re.search(r"[\u0400-\u04FF]", raw):
        return False
    bak = backup_path(path)
    if bak and not os.path.exists(bak):
        os.makedirs(os.path.dirname(bak), exist_ok=True)
        shutil.copy2(path, bak)
    tmp = path + ".lat.tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        f.write(cyr2lat(raw))
    os.chmod(tmp, os.stat(path).st_mode & 0o777)
    os.replace(tmp, path)
    return True


def looks_machine_translated(path):
    """Text-quality tell for sr/hr/bs subs, calibrated on a season of machine
    translations against credited human files from titlovi.com: MT has
    no translator credit, lots of stubby fragment cues, short average lines,
    and never merges two speakers with dialogue dashes.  Human subs fail at
    least one of the four.  Returns a reason string, or None if it looks fine.
    """
    texts = [re.sub(r"<[^>]+>", "", t).strip()
             for _, _, t in parse_srt(path)]
    texts = [t.replace("\n", " ") for t in texts if t]
    if len(texts) < 40:            # too little text to judge (intros, tests)
        return None
    if MT_CREDIT.search(" ".join(texts[-12:] + texts[:5])):
        return None
    short = sum(1 for t in texts if len(t) <= 10) / len(texts)
    dash = sum(1 for t in texts if t.startswith("-")) / len(texts)
    avg = sum(len(t) for t in texts) / len(texts)
    if short >= 0.21 and dash < 0.02 and avg < 26:
        return (f"no translator credit, {short:.0%} fragment cues, "
                f"avg line {avg:.0f} chars, no dialogue dashes - reads like "
                "a machine translation")
    return None


def fmt_ts(t):
    t = max(t, 0.0)
    ms = round(t * 1000)
    return f"{ms//3600000:02d}:{ms//60000%60:02d}:{ms//1000%60:02d},{ms%1000:03d}"


def write_srt(cues, path):
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n\n".join(
            f"{i+1}\n{fmt_ts(s)} --> {fmt_ts(e)}\n{t}"
            for i, (s, e, t) in enumerate(cues)) + "\n")


def segment_cues(cues, boundaries):
    segs = [[] for _ in range(len(boundaries) + 1)]
    for c in cues:
        i = sum(1 for b in boundaries if c[0] >= b)
        segs[i].append(c)
    # merge undersized segments into their predecessor (or successor for the first)
    merged = []
    for s in segs:
        if merged and (len(s) < MIN_CUES_PER_SEG or len(merged[-1]) < MIN_CUES_PER_SEG):
            merged[-1] = merged[-1] + s
        else:
            merged.append(s)
    return [s for s in merged if s]


def ffsubsync_run(reference, srt_in, srt_out, maxoff=MAX_OFFSET):
    """Invoke vendored ffsubsync; returns (ok, offset_seconds, score)."""
    code = (
        _ffs_prelude() +
        f"sys.argv=['ffsubsync',{reference!r},'-i',{srt_in!r},'-o',{srt_out!r},"
        f"'--no-fix-framerate','--max-offset-seconds',{maxoff!r}]\n"
        "from ffsubsync.ffsubsync import main\nsys.exit(main())\n"
    )
    r = run([sys.executable, "-c", code])
    out = r.stdout + r.stderr
    m = re.search(r"offset seconds:\s*([-\d.]+)", out)
    sc = re.search(r"score:\s*([\d.]+)", out)
    ok = r.returncode == 0 and m is not None
    return ok, float(m.group(1)) if m else None, float(sc.group(1)) if sc else 0.0


def audio_channels(video):
    r = run(["ffprobe", "-v", "error", "-select_streams", "a:0",
             "-show_entries", "stream=channels", "-of", "csv=p=0", video])
    try:
        return int((r.stdout or "0").strip().splitlines()[0])
    except (ValueError, IndexError):
        return 0


def speech_density(npz):
    """Fraction of the runtime the detector heard speech in. A real episode
    lands 0.2-0.7; near zero means the audio never decoded, the codec was not
    understood, or the track is silent — in every one of those cases there is
    nothing to align against and measuring is self-deception."""
    try:
        import numpy as np
        return float(np.load(npz, allow_pickle=True)["speech"].astype(float).mean())
    except Exception:
        return 1.0      # cannot tell -> do not block the sync on this check


def peak_coverage(npz, cues, reach=30.0, step=0.25):
    """Best speech coverage this subtitle can reach against a reference.

    Used to judge a REFERENCE, not a subtitle: a reference that genuinely
    captures the dialogue lets a real subtitle score high somewhere. A centre
    channel that is empty, upmixed, or noise-boosted cannot, however loud it is.
    """
    try:
        import numpy as np
        speech = np.load(npz, allow_pickle=True)["speech"].astype(float)
    except Exception:
        return 0.0
    csum = np.concatenate(([0.0], np.cumsum(speech)))
    total = len(speech)
    best = 0.0
    d = -reach
    while d <= reach:
        hit = span = 0.0
        for s, e, _ in cues:
            a, b = int((s + d) * 100.0), int((e + d) * 100.0)
            a, b = max(a, 0), min(b, total)
            if b > a:
                hit += csum[b] - csum[a]
                span += b - a
        if span:
            best = max(best, hit / span)
        d += step
    return best


def choose_reference(video, subtitle_cues, workdir, result):
    """Pick the speech reference that actually explains this video's dialogue.

    Neither reference wins everywhere — measured on two files from the same
    show: the centre channel rescued a whispered night scene the full downmix
    could not see at all (0.17 -> 0.49), while on another episode the centre
    channel was far worse than the mix (0.38 vs 0.95) and drove the aligner to
    a false 26s "correction". So build both and let the audio decide.
    """
    mix = speech_npz(video, workdir, centre=False)
    if not CENTRE_REF or audio_channels(video) < 6:
        return mix
    decision_file = cache_key(video, "refchoice.json")
    try:
        v = json.load(open(decision_file))
        if v.get("schema") == CENTRE_REF_TAG:
            result["speech_reference"] = v["choice"]
            # the stored path may come from another mount of the same cache
            # (e.g. a cache built inside Bazarr's container): look it up here
            npz = os.path.join(CACHE_DIR, os.path.basename(v["npz"]))
            return npz if os.path.exists(npz) else mix
    except (OSError, ValueError, KeyError):
        pass
    sample = subtitle_cues[:400]
    q_mix = peak_coverage(mix, sample)
    if q_mix >= CENTRE_TRIGGER:
        # The mix already explains this dialogue well (0.95 is common on clean
        # releases). Building the centre reference costs a whole extra audio
        # decode and, where the mix is this strong, only risks replacing a good
        # reference with a worse one — which is exactly how a false 26s shift
        # got proposed on a perfectly aligned episode.
        result["speech_reference"] = "mix"
        try:
            json.dump({"schema": CENTRE_REF_TAG, "choice": "mix", "npz": mix,
                       "mix": round(q_mix, 3), "centre": None},
                      open(decision_file, "w"))
        except OSError:
            pass
        return mix
    centre = speech_npz(video, workdir, centre=True)
    if centre == mix:
        return mix
    q_centre = peak_coverage(centre, sample)
    win = centre if q_centre > q_mix + CENTRE_MIN_EDGE else mix
    choice = "centre" if win is centre else "mix"
    result["speech_reference"] = choice
    result["reference_quality"] = {"mix": round(q_mix, 3),
                                   "centre": round(q_centre, 3)}
    try:
        json.dump({"schema": CENTRE_REF_TAG, "choice": choice, "npz": win,
                   "mix": round(q_mix, 3), "centre": round(q_centre, 3)},
                  open(decision_file, "w"))
    except OSError:
        pass
    return win


def speech_npz(video, workdir, centre=None):
    """One audio decode per video, cached in CACHE_DIR.

    On 5.1 audio the dialogue is (almost) alone in the centre channel, so the
    speech detector is fed that channel, loudness-levelled. Whispered scenes
    under music/ambience — which the full downmix hides entirely — then survive
    detection. Measured on TWD S02E12's night-forest scene: speech coverage
    0.15 -> 0.49, turning a 3.5s error that read as noise (margin +0.007 over
    the runner-up) into an unambiguous one (+0.177)."""
    os.makedirs(CACHE_DIR, exist_ok=True)
    st = os.stat(video)
    if centre is None:
        centre = CENTRE_REF and audio_channels(video) >= 6
    # stereo keeps the legacy key so its existing cache stays valid
    key = (f"{os.path.basename(video)}.{st.st_size}"
           f"{'.' + CENTRE_REF_TAG if centre else ''}.npz")
    cached = os.path.join(CACHE_DIR, key)
    if os.path.exists(cached):
        return cached
    if centre:
        link = os.path.join(workdir, "centre.wav")
        r = run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
                 "-i", video, "-map", "0:a:0", "-af", CENTRE_FILTER,
                 "-ar", "16000", "-ac", "1", link])
        if r.returncode != 0 or not os.path.exists(link):
            centre = False           # fall back to the full mix
    if not centre:
        # Decode the audio ourselves rather than handing ffsubsync the video.
        # Letting it extract internally silently yields a near-empty speech
        # track on some files — measured on Big Bang Theory AMZN E-AC-3 rips
        # with two audio streams: 0.003 density through ffsubsync's own
        # extraction, 0.649 from the very same stream pulled out with ffmpeg
        # first. That silence is indistinguishable from "quiet film" and is
        # what let 37 subtitles get "corrected" by up to 47s of noise.
        link = os.path.join(workdir, "mix.wav")
        r = run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
                 "-i", video, "-map", "0:a:0", "-ar", "16000", "-ac", "1", link])
        if r.returncode != 0 or not os.path.exists(link):
            # last resort: the old behaviour, so a file we cannot demux still
            # gets a chance rather than failing outright
            link = os.path.join(workdir, "ref" + os.path.splitext(video)[1])
            os.symlink(video, link)
    dummy_in = os.path.join(workdir, "dummy.srt")
    open(dummy_in, "w").write("1\n00:00:01,000 --> 00:00:02,000\nx\n")
    code = (
        _ffs_prelude() +
        f"sys.argv=['ffsubsync',{link!r},'-i',{dummy_in!r},"
        f"'-o',{os.path.join(workdir,'dummy_out.srt')!r},'--serialize-speech',"
        "'--vad','webrtc']\n"
        # --vad webrtc is LOAD-BEARING: ffsubsync's default is subs_then_webrtc,
        # which silently uses an EMBEDDED SUBTITLE TRACK as the "speech" reference
        # when the reference is a video. Every pre-08-13 cache built via the
        # symlink route was the first English track's cue mask, so all audio
        # checks against it were circular (TWD S05E01: wrong track, 5.2s late,
        # "verified" perfect for two weeks; found 2026-08-27).
        "from ffsubsync.ffsubsync import main\nsys.exit(main())\n"
    )
    r = run([sys.executable, "-c", code])
    produced = link + ".npz"
    if not os.path.exists(produced):
        cand = [os.path.join(workdir, f) for f in os.listdir(workdir) if f.endswith(".npz")]
        if not cand:
            raise RuntimeError("speech serialization failed:\n" + r.stderr[-800:])
        produced = cand[0]
    shutil.move(produced, cached)
    return cached


def find_embedded_text_track(video):
    """First candidate (legacy callers)."""
    c = find_embedded_text_tracks(video)
    return c[0] if c else None


def find_embedded_text_tracks(video):
    """Indices of ALL full English/unlabelled text subtitle streams, in stream
    order. Releases can carry several (Joy TWD S05E01: track 2 is from another
    cut, 5.2s late; track 3 is right) — the caller must test each against the
    audio and keep the best, never just the first."""
    r = run(["ffprobe", "-v", "error", "-show_entries",
             "stream=index,codec_type,codec_name:stream_tags=language,title"
             ":stream_disposition=forced", "-of", "json", video])
    try:
        streams = json.loads(r.stdout or "{}").get("streams", [])
    except ValueError:
        return []
    found = []
    for st in streams:
        if st.get("codec_type") != "subtitle":
            continue
        if st.get("codec_name") not in TEXT_SUB_CODECS:
            continue
        tags = {k.lower(): str(v).lower() for k, v in (st.get("tags") or {}).items()}
        if tags.get("language", "eng") not in ("eng", "en", "und", ""):
            continue
        if any(w in tags.get("title", "")
               for w in ("commentary", "forced", "signs", "songs")):
            continue
        if (st.get("disposition") or {}).get("forced"):
            continue
        found.append(st["index"])
    return found


def cache_key(video, suffix):
    st = os.stat(video)
    return os.path.join(CACHE_DIR, f"{os.path.basename(video)}.{st.st_size}.{suffix}")


def extract_ref(video, idx, workdir):
    """Demux an embedded text track to SRT, cached per video: ffmpeg must read
    the WHOLE file to demux a subtitle stream, so a remux on the NAS costs
    minutes — and every other language of the same video would pay it again."""
    cached = cache_key(video, f"track{idx}.ref.srt")
    if os.path.exists(cached):
        return cached if os.path.getsize(cached) else None
    ref = os.path.join(workdir, "ref.srt")
    r = run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", video,
             "-map", f"0:{idx}", "-c:s", "srt", ref])
    os.makedirs(CACHE_DIR, exist_ok=True)
    if r.returncode != 0 or not os.path.exists(ref):
        open(cached, "w").close()   # empty = "extraction failed", do not re-read
        return None
    shutil.copy(ref, cached)
    return cached


def measure_ref(npz, ref, duration, workdir):
    """Does an embedded track agree with this file's own audio? (ok, why)"""
    ok_r, off_r, _ = ffsubsync_run(npz, ref,
                                   os.path.join(workdir, "refcheck.srt"),
                                   maxoff=CAP_NARROW)
    if not ok_r or abs(off_r) > REF_MAX_AUDIO_OFFSET:
        return False, f"disagrees with audio (offset {off_r if ok_r else 'unmeasurable'})", off_r
    # Whole-file agreement is NOT enough: a wrong-cut track with stepped drift
    # (Joy S02E12: acts run 0 -> +11.6s) still measures ~1s overall, because the
    # early acts dominate the vote. The drift accumulates, so the last ~30% must
    # independently agree too.
    tail = [c for c in parse_srt(ref) if c[0] >= duration * 0.7]
    if len(tail) >= MIN_CUES_PER_SEG:
        tail_in = os.path.join(workdir, "reftail.srt")
        write_srt(tail, tail_in)
        ok_t, off_t, _ = ffsubsync_run(npz, tail_in,
                                       os.path.join(workdir, "reftail_out.srt"),
                                       maxoff=CAP_NARROW)
        if not ok_t or abs(off_t) > REF_MAX_AUDIO_OFFSET:
            return False, f"tail disagrees with audio (offset {off_t if ok_t else 'unmeasurable'})", off_r
    return True, f"agrees with audio (offset {off_r})", off_r


def ref_audio_ok(video, npz, ref, duration, workdir, idx=0):
    """measure_ref, cached per (video, track): the verdict is a property of the
    video, not of the subtitle being synced, so all languages share it."""
    cached = cache_key(video, f"track{idx}.refcheck.json")
    try:
        v = json.load(open(cached))
        if v.get("schema") == REFCHECK_SCHEMA:
            return v["ok"], v["why"], v.get("offset")
    except (OSError, ValueError, KeyError):
        pass
    ok, why, off = measure_ref(npz, ref, duration, workdir)
    try:
        os.makedirs(CACHE_DIR, exist_ok=True)
        json.dump({"schema": REFCHECK_SCHEMA, "ok": ok, "why": why,
                   "offset": off}, open(cached, "w"))
    except OSError:
        pass
    return ok, why, off


def alass_align(ref, subtitle, workdir):
    """Align subtitle to a reference track; returns (cues, deltas) or (None, None).
    alass handles arbitrary cut insertions (block splits, unlimited total
    drift) that the +-40s audio-correlation search cannot represent."""
    out = os.path.join(workdir, "alass_out.srt")
    try:
        r = run([ALASS, ref, subtitle, out], timeout=300)
    except subprocess.TimeoutExpired:
        return None, None
    if r.returncode != 0 or not os.path.exists(out):
        return None, None
    orig, new = parse_srt(subtitle), parse_srt(out)
    if len(orig) != len(new):
        return None, None
    deltas = [n[0] - o[0] for o, n in zip(orig, new)]
    return new, deltas


def backup_path(subtitle):
    """Where the first original of this subtitle is kept (chosen by the server,
    which mirrors the library tree so same-named files never collide)."""
    return BACKUP_PATH


def polish_residual(cues, npz, result):
    """Catch a short displaced block that act-sized segments cannot see.

    A segment offset is a majority vote: 11 cues sitting 3.5s early inside a
    51-cue act are outvoted and the act reports ~0. So each cue is re-measured
    against a small neighbourhood of its own, directly on the speech track.

    Every historical failure of fine-grained syncing came from acting on a
    measurement that was noise, so a shift is applied only when ALL hold:
      - it improves speech coverage by a real margin (not a rounding win),
      - it beats its nearest rival clearly (a flat curve means no information),
      - the result actually lands on speech (a blind scene stays blind - this
        is what stops the tool "fixing" music-only stretches), and
      - neighbouring cues independently agree (one cue alone is never enough).
    """
    try:
        import numpy as np
    except ImportError:
        return cues
    try:
        speech = np.load(npz, allow_pickle=True)["speech"].astype(float)
    except (OSError, ValueError, KeyError):
        return cues

    n = len(cues)
    if n < POLISH_GROUP:
        return cues
    fps = 100.0
    total = len(speech)

    # prefix sums: a coverage probe becomes two lookups instead of summing a
    # slice — this pass evaluates ~80k of them per subtitle
    csum = np.concatenate(([0.0], np.cumsum(speech)))

    def coverage(sel, shift):
        hit = span = 0.0
        for s, e, _ in sel:
            a, b = int((s + shift) * fps), int((e + shift) * fps)
            a, b = max(a, 0), min(b, total)
            if b > a:
                hit += csum[b] - csum[a]
                span += b - a
        return hit / span if span else 0.0

    grid = [round(-POLISH_MAX + i * 0.05, 2)
            for i in range(int(2 * POLISH_MAX / 0.05) + 1)]
    proposed = [0.0] * n
    for i in range(n):
        lo = max(0, min(i - POLISH_GROUP // 2, n - POLISH_GROUP))
        sel = cues[lo:lo + POLISH_GROUP]
        scores = [coverage(sel, d) for d in grid]
        bi = max(range(len(grid)), key=scores.__getitem__)
        best, shift = scores[bi], grid[bi]
        rival = max((scores[k] for k in range(len(grid))
                     if abs(grid[k] - shift) > 1.0), default=0.0)
        if (abs(shift) >= POLISH_MIN_SHIFT
                and best - coverage(sel, 0.0) >= POLISH_MIN_GAIN
                and best - rival >= POLISH_MIN_MARGIN
                and best >= POLISH_MIN_COVERAGE):
            proposed[i] = shift

    # a lone cue is never enough: keep only runs where neighbours concur
    keep = [0.0] * n
    i = 0
    while i < n:
        if proposed[i] == 0.0:
            i += 1
            continue
        j = i
        while j + 1 < n and abs(proposed[j + 1] - proposed[i]) <= 0.5:
            j += 1
        if j - i + 1 >= POLISH_MIN_RUN:
            run = proposed[i:j + 1]
            for k in range(i, j + 1):
                keep[k] = sum(run) / len(run)
        i = j + 1

    # corroboration: a single short block standing alone is the shape every
    # historical false positive took. Keep it only if it is substantial, or if
    # another block independently found a similar shift.
    runs = []
    i = 0
    while i < n:
        if keep[i] == 0.0:
            i += 1
            continue
        j = i
        while j + 1 < n and keep[j + 1] == keep[i]:
            j += 1
        runs.append((i, j, keep[i]))
        i = j + 1
    for a, b, sh in runs:
        if b - a + 1 >= POLISH_CORROBORATE:
            continue
        if any(o is not (a, b, sh) and abs(o[2] - sh) <= 1.0
               for o in runs if (o[0], o[1]) != (a, b)):
            continue
        for k in range(a, b + 1):
            keep[k] = 0.0

    if not any(keep):
        return cues
    fixed = [(s + d, e + d, t) for (s, e, t), d in zip(cues, keep)]
    fixed.sort()
    blocks, i = [], 0
    while i < n:
        if keep[i] == 0.0:
            i += 1
            continue
        j = i
        while j + 1 < n and keep[j + 1] == keep[i]:
            j += 1
        blocks.append({"from": round(cues[i][0], 1), "to": round(cues[j][1], 1),
                       "cues": j - i + 1, "shift": round(keep[i], 2)})
        i = j + 1
    if not POLISH_APPLY:
        result["polish_proposed"] = blocks      # evidence only, file untouched
        return cues
    result["polished"] = blocks
    # the noop check reads max_shift; without this a file whose ONLY error is a
    # displaced block is measured, corrected in memory, and then thrown away
    result["max_shift"] = round(max([result.get("max_shift") or 0.0]
                                    + [abs(b["shift"]) for b in blocks]), 2)
    return fixed


def coverage_at(npz, cues, d=0.0):
    """Speech coverage of these cues when shifted by d seconds (no search)."""
    try:
        import numpy as np
        speech = np.load(npz, allow_pickle=True)["speech"].astype(float)
    except Exception:
        return 0.0
    csum = np.concatenate(([0.0], np.cumsum(speech)))
    total = len(speech)
    hit = span = 0.0
    for s, e, _ in cues:
        a, b = int((s + d) * 100.0), int((e + d) * 100.0)
        a, b = max(a, 0), min(b, total)
        if b > a:
            hit += csum[b] - csum[a]
            span += b - a
    return hit / span if span else 0.0


def residual_correct(cues, npz, video, duration, workdir, result):
    """Take the embedded track's own lag back out of an alass result.

    alass copies the reference track's timing exactly - including however far
    that track sits from the audio. The refcheck tolerates 1.5s, and Joy S05E02's
    track drifts from 0 to -0.85s across the episode, so alass output inherited
    up to ~0.85s of lag (plus the reading lead => "subtitles are late",
    2026-08-26). Re-measure each act of the aligned output against the audio
    with a tight search and remove what is left. Guards: per-act shift within
    [REF_RESIDUAL_MIN, REF_RESIDUAL_MAX] and not pinned at the search cap, and
    the cues must land on MORE speech afterwards (blind scenes fail this and
    stay put)."""
    boundaries = detect_boundaries(video, duration)
    segs = segment_cues(cues, boundaries)
    out, applied, raw = [], [], []
    for i, seg in enumerate(segs):
        seg_in = os.path.join(workdir, f"res{i}.srt")
        seg_out = os.path.join(workdir, f"res{i}_out.srt")
        write_srt(seg, seg_in)
        ok, off, _ = ffsubsync_run(npz, seg_in, seg_out, maxoff=REF_RESIDUAL_CAP)
        shift, gain = 0.0, None
        if ok and REF_RESIDUAL_MIN <= abs(off) <= REF_RESIDUAL_MAX \
                and abs(abs(off) - float(REF_RESIDUAL_CAP)) >= CAP_EPS:
            gain = round(coverage_at(npz, seg, off) - coverage_at(npz, seg, 0.0), 3)
            if gain >= REF_RESIDUAL_MIN_GAIN:
                shift = off
        applied.append(round(shift, 2))
        raw.append([len(seg), round(off, 2) if ok else None, gain])
        out.extend((s + shift, e + shift, t) for s, e, t in seg)
    result["ref_residual"] = applied
    result["ref_residual_raw"] = raw   # [cues, measured, coverage gain] per act
    return out


def align_measure(video, subtitle, workdir, result):
    """Measure everything; returns (out_cues, giveup_reason)."""
    duration = ffprobe_duration(video)
    cues = parse_srt(subtitle)
    if len(cues) < MIN_CUES:
        return None, f"only {len(cues)} cues parsed - not a usable subtitle"
    stage("reading the audio")
    npz = choose_reference(video, cues, workdir, result)

    # An empty reference is worse than no reference: ffsubsync still returns an
    # offset, the piecewise pass still builds segments, and noise comes back
    # dressed as a measurement. Seen 2026-08-13 on Big Bang Theory DDP2.0 rips
    # whose speech track came out at 0.003 density — three subtitles were
    # "corrected" by 8-47s and ruined. Healthy references run 0.2-0.7.
    density = speech_density(npz)
    result["speech_density"] = round(density, 3)
    result["_npz"] = npz    # for the content-fit gate; stripped before output
    if density < MIN_SPEECH_DENSITY:
        return None, (f"speech reference is empty (density {density:.3f}) - no "
                      "detectable speech in this audio, so any offset would be "
                      "noise")

    # Preferred path: align against an embedded text subtitle track from the
    # same file. A disc-sourced track is exact for THIS cut, and alass can
    # follow arbitrary scene insertions (theatrical sub on a Director's Cut
    # drifts by minutes - far beyond the +-40s the audio search can model,
    # where ffsubsync returns a false-confident ~0 instead of failing).
    # The track must first prove it matches the audio: Joy-style re-encodes
    # mux tracks from the WRONG cut, and those must not be trusted.
    passing, rejected = [], []
    tracks = find_embedded_text_tracks(video)
    if tracks:
        stage(f"checking {len(tracks)} embedded track{'s' if len(tracks) > 1 else ''} against the audio")
    for idx in tracks:
        ref = extract_ref(video, idx, workdir)
        if not ref or len(parse_srt(ref)) < MIN_CUES:
            continue
        ref_ok, why, off = ref_audio_ok(video, npz, ref, duration, workdir, idx)
        if ref_ok:
            passing.append((abs(off or 0.0), idx, ref, off))
        else:
            rejected.append(f"track {idx} {why}")
    if rejected:
        result["embedded_track_rejected"] = "; ".join(rejected)
    if passing:
        passing.sort()
        _, idx, ref, off = passing[0]
        result["embedded_track"] = idx
        result["embedded_track_offset"] = off
        if True:
            if True:
                # baseline for the content-fit gate: how well a subtitle KNOWN
                # to belong to this video covers the detected speech
                result["ref_fit"] = round(
                    peak_coverage(npz, parse_srt(ref),
                                  reach=FIT_REACH, step=FIT_STEP), 3)
                stage(f"aligning to embedded track {idx}")
                new_cues, deltas = alass_align(ref, subtitle, workdir)
                if new_cues and max(e for _, e, _ in new_cues) <= duration + 10:
                    result["method"] = "alass_embedded_track"
                    result["stepped_cut"] = max(deltas) - min(deltas) > 1.5
                    # Lines from a cut this video lacks ("Previously on…")
                    # cannot be matched; alass parks them at t=0 where they
                    # flash over the first frame. Three or more cues sharing
                    # a start inside the first half-second is that signature.
                    stacked = [c for c in new_cues if c[0] < 0.5]
                    if len(stacked) >= 3 and \
                            max(c[0] for c in stacked) - min(c[0] for c in stacked) < 0.05:
                        keep = [c for c in new_cues if c[0] >= 0.5]
                        result["dropped_unmatched"] = len(new_cues) - len(keep)
                        new_cues = keep
                    if REF_RESIDUAL:
                        new_cues = residual_correct(new_cues, npz, video,
                                                    duration, workdir, result)
                    if len(new_cues) == len(cues):
                        deltas = [n[0] - o[0] for o, n in zip(cues, new_cues)]
                    result["max_shift"] = round(max(abs(d) for d in deltas), 2)
                    return new_cues, None

    result["method"] = "audio_piecewise"
    stage("finding act breaks")
    boundaries = detect_boundaries(video, duration)

    # Whole-file alignment first: it uses every cue, so it is far more
    # reliable than any single segment. It anchors what offsets are
    # plausible — a segment claiming to be 30s away from the whole-file
    # answer is finding noise, not a cut.
    stage("measuring the whole file")
    whole_in = os.path.join(workdir, "whole.srt")
    write_srt(cues, whole_in)
    ok_g, off_g, _ = ffsubsync_run(npz, whole_in,
                                   os.path.join(workdir, "whole_out.srt"),
                                   maxoff=CAP_NARROW)
    if ok_g and abs(abs(off_g) - float(CAP_NARROW)) < CAP_EPS:
        ok_g = False
    if not ok_g:
        # Grossly offset subs (wrong-release rips) sit beyond +-15s; retry at
        # the widest trusted search before giving up.
        ok_g, off_g, _ = ffsubsync_run(npz, whole_in,
                                       os.path.join(workdir, "whole_wide.srt"))
        if ok_g and abs(abs(off_g) - float(MAX_OFFSET)) < CAP_EPS:
            ok_g = False
    if not ok_g:
        return None, "whole-file alignment found no trustworthy offset"
    global_off = off_g
    result["global_offset"] = round(global_off, 2)
    segs = segment_cues(cues, boundaries)
    result["boundaries"] = [round(b, 1) for b in boundaries]
    result["segments"] = []

    # Dual-search per segment. ffsubsync's score CANNOT arbitrate large
    # offsets (genuine +25s scores 1.01-1.08x its false small alternative;
    # false +86s scores 1.09-1.18x the truth — measured, overlapping).
    # What separates them is neighbor agreement: real cut steps form
    # plateaus (adjacent acts agree within seconds), false attractors
    # scatter (E11: +90/+74.9/+86.7). A large free-search offset is
    # accepted only when an adjacent segment's free search lands within
    # 5s of it; otherwise the capped (±15s) result is used.
    aligned = []
    for i, seg in enumerate(segs):
        stage(f"measuring act {i + 1} of {len(segs)}")
        seg_in = os.path.join(workdir, f"seg{i}.srt")
        seg_out = os.path.join(workdir, f"seg{i}_out.srt")
        write_srt(seg, seg_in)
        ok_c, off_c, sc_c = ffsubsync_run(npz, seg_in, seg_out, maxoff=CAP_NARROW)
        ok_f, off_f, sc_f = ffsubsync_run(npz, seg_in, seg_out)
        # An offset sitting on a search bound is not a measurement, it is the
        # search hitting the wall — discard it.
        if ok_c and abs(abs(off_c) - float(CAP_NARROW)) < CAP_EPS:
            ok_c, off_c = False, None
        if ok_f and abs(abs(off_f) - float(MAX_OFFSET)) < CAP_EPS:
            ok_f, off_f = False, None
        # Degenerate segment: widening the search moved the answer but did
        # NOT improve the score => the correlation surface is flat, there is
        # no real peak (segment carries music/atmosphere, not dialogue).
        # Any offset it reports is noise; defer to neighbours.
        degenerate = (ok_c and ok_f and abs(off_f - off_c) > 2
                      and abs(sc_f - sc_c) <= 0.001 * max(sc_f, sc_c, 1))
        if not ok_c and not ok_f:
            degenerate = True
        aligned.append({"seg": seg, "synced": ok_c or ok_f,
                        "free": off_f if ok_f else None,
                        "capped": off_c if ok_c else None,
                        "degenerate": degenerate})
    free = [a["free"] for a in aligned]
    n = len(aligned)
    for i, a in enumerate(aligned):
        if a["free"] is None and a["capped"] is None:
            a["offset"] = aligned[i - 1]["offset"] if i else global_off
            continue
        if a["free"] is None or a["capped"] is None:
            a["offset"] = a["free"] if a["free"] is not None else a["capped"]
            continue
        if abs(a["free"] - a["capped"]) < 2:
            a["offset"] = a["free"]
            continue
        # A large free-search offset is only trusted when a neighbour
        # independently lands near it — and that neighbour must itself be a
        # real measurement, not a degenerate/pinned one (else two pinned
        # segments "confirm" each other into a fake plateau).
        confirmed = any(
            0 <= j < n and j != i and free[j] is not None
            and not aligned[j].get("degenerate")
            and abs(a["free"] - free[j]) < 5
            for j in (i - 1, i + 1))
        a["offset"] = a["free"] if confirmed else a["capped"]

    # Outlier rejection: cut edits accumulate, so a lone segment that
    # jumps far away while its neighbors agree is a false alignment
    # (dialogue-sparse segment latching onto the wrong speech). Replace
    # its offset with the neighbors' mean.
    offs = [a["offset"] for a in aligned]
    n = len(offs)
    # pass -1: degenerate segments (flat correlation, no real peak) take
    # the nearest confident neighbour's offset rather than their own noise.
    confident = [i for i in range(n) if not aligned[i].get("degenerate")]
    for i in range(n):
        if aligned[i].get("degenerate"):
            if confident:
                j = min(confident, key=lambda k: abs(k - i))
                offs[i] = offs[j]
            else:
                offs[i] = global_off
            aligned[i]["outlier"] = True
    # pass 0a: anchor check — no segment may sit further than MAX_DEVIATION
    # from the whole-file answer. Real cut inserts are bounded (recaps,
    # act padding); a 35s excursion from a globally-aligned file is noise.
    for i, a in enumerate(aligned):
        if abs(offs[i] - global_off) > MAX_DEVIATION:
            cand = a["capped"]
            offs[i] = cand if (cand is not None
                               and abs(cand - global_off) <= MAX_DEVIATION) \
                else global_off
            aligned[i]["outlier"] = True
    # pass 0b: suffix/physics check. Content inserted into the video shifts
    # every LATER cue by at least as much — the displacement persists to the
    # end of the file. So a mid-file segment may only claim a large offset
    # if the final segment is displaced the same way and at least half as
    # far. Noise fails this: it spikes mid-file and returns to zero.
    tail = offs[-1] - global_off
    for i in range(n - 1):
        dev = offs[i] - global_off
        if abs(dev) <= 3:
            continue
        persists = (dev * tail > 0) and abs(tail) >= 0.5 * abs(dev)
        if not persists:
            cand = aligned[i]["capped"]
            offs[i] = cand if (cand is not None
                               and abs(cand - global_off) <= 3) else global_off
            aligned[i]["outlier"] = True
    # pass 0: cap-edge suspects — an offset pinned at the capped-search
    # boundary (~15s) that no neighbor agrees with is a false attractor
    # (the capped fallback is only safer, not safe; seen on HDM S1).
    for i in range(n):
        nb = [offs[j] for j in (i - 1, i + 1) if 0 <= j < n]
        if abs(offs[i]) >= 14 and nb and all(abs(offs[i] - x) > 8 for x in nb):
            offs[i] = sum(nb) / len(nb)
            aligned[i]["outlier"] = True
    # pass 1: interior segments — far from BOTH neighbors while the
    # neighbors agree with each other means a false alignment
    for i in range(1, n - 1):
        lo, hi = offs[i - 1], offs[i + 1]
        if abs(offs[i] - lo) > 20 and abs(offs[i] - hi) > 20 and abs(lo - hi) < 5:
            offs[i] = (lo + hi) / 2
            aligned[i]["outlier"] = True
    # pass 2: physical guard — a shift may not push any cue past the end
    # of the video (or before its start); such an alignment is false.
    # Fall back to the segment's own capped-search result, then neighbor.
    for i, a in enumerate(aligned):
        last_end = max(e for _, e, _ in a["seg"])
        first_start = min(s for s, _, _ in a["seg"])
        def violates(o):
            return last_end + o > duration + 10 or first_start + o < -2
        if violates(offs[i]):
            cand = a["capped"] if a["capped"] is not None else None
            if cand is not None and not violates(cand):
                offs[i] = cand
            else:
                offs[i] = offs[i - 1] if i and not violates(offs[i - 1]) else 0.0
            aligned[i]["outlier"] = True
    for a, o in zip(aligned, offs):
        a["offset"] = o

    out_cues = []
    for a in aligned:
        out_cues.extend((s + a["offset"], e + a["offset"], t)
                        for s, e, t in a["seg"])
        result["segments"].append(
            {"cues": len(a["seg"]), "offset": round(a["offset"], 2),
             "synced": a["synced"], "outlier": a.get("outlier", False)})
    out_cues.sort()
    offsets = [s["offset"] for s in result["segments"]]
    result["stepped_cut"] = max(offsets) - min(offsets) > 1.5
    result["max_shift"] = round(max(abs(o) for o in offsets), 2)
    stage("polishing short displaced blocks")
    out_cues = polish_residual(out_cues, npz, result)
    return out_cues, None


def align(video, subtitle, workdir, result):
    """align_measure, plus the reading lead — applied on every path.

    The lead goes on last, after alignment and polish, so no measurement ever
    sees it. net_shift is how far the file still has to move to sit at the
    target: it is derived from the cues themselves rather than from segment
    offsets, because the embedded-track path never produces segments. A file
    already carrying the lead measures a delta of -LEAD_OUT, which nets to
    zero — that is what stops repeat runs stacking lead on lead.
    """
    before = parse_srt(subtitle)
    out_cues, giveup = align_measure(video, subtitle, workdir, result)
    if giveup:
        return out_cues, giveup
    if len(out_cues) == len(before):
        deltas = [o[0] - c[0] for c, o in zip(before, out_cues)]
        result["net_shift"] = round(max(abs(d + LEAD_OUT) for d in deltas), 2)
    else:
        result["net_shift"] = round(
            abs(result.get("max_shift", 0.0)) + abs(LEAD_OUT), 2)
    if LEAD_OUT:
        result["lead_out"] = LEAD_OUT
        out_cues = [(s + LEAD_OUT, e + LEAD_OUT, t) for s, e, t in out_cues]
    return out_cues, None


def content_fit_verdict(npz, out_cues, result):
    """None if the cues plausibly belong to this video, else a reason."""
    fit = round(peak_coverage(npz, out_cues, reach=FIT_REACH, step=FIT_STEP), 3)
    result["content_fit"] = fit
    ref_fit = result.get("ref_fit")
    # ref_fit comes from an embedded track that already PROVED it matches this
    # audio, so a low ref_fit means the speech detection is unreliable for this
    # video (score/action drowning the dialogue - The Dark Knight measured 17%
    # on its own track). There the absolute floor rejects correct subtitles;
    # judge only against what the video's own track manages.
    ref_healthy = ref_fit is None or ref_fit >= FIT_MIN_REF
    wrong = ((fit < FIT_FLOOR and ref_healthy)
             or (ref_fit is not None and fit < FIT_RATIO * ref_fit))
    if not wrong:
        return None
    return (f"cues cover only {fit:.0%} of the detected speech"
            + (f" (this video's embedded track covers {ref_fit:.0%})"
               if ref_fit is not None else "")
            + " - the text belongs to a different episode/movie")


def cmd_sync(args):
    global LEAD_OUT, CENTRE_REF, POLISH_APPLY, REF_RESIDUAL, BACKUP_PATH
    LEAD_OUT = args.lead
    CENTRE_REF, POLISH_APPLY, REF_RESIDUAL = args.centre_ref, args.polish, args.ref_residual
    BACKUP_PATH = args.backup_path

    result = {"video": os.path.basename(args.video),
              "subtitle": os.path.basename(args.subtitle),
              "changed": False}

    if not args.subtitle.lower().endswith(".srt"):
        result["decision"] = "skipped"
        result["reason"] = "not an srt file"
        return result

    # Script normalisation first: text-only, deterministic, and the MT gate
    # below is calibrated on Latin text.
    if args.latinize and not args.dry_run:
        try:
            if latinize_if_cyrillic(args.subtitle):
                result["latinized"] = True
        except OSError as e:
            result["latinize_error"] = str(e)

    if args.pinned:
        # Hand-corrected timing: alignment must not touch it.
        result["decision"] = "pinned"
        result["reason"] = "pinned - timing is left exactly as it is"
        return result

    # Machine-translation gate (fresh downloads only: the server passes
    # --mt-gate when a provider just delivered the file). Text-only, so it runs
    # before any audio work.
    if args.mt_gate:
        mt_reason = looks_machine_translated(args.subtitle)
        if mt_reason:
            result["decision"] = "rejected_machine_translation"
            result["reason"] = mt_reason
            return result

    workdir = tempfile.mkdtemp(prefix="pocket-sync.")
    try:
        stage("reading the subtitle")
        out_cues, giveup = align(args.video, args.subtitle, workdir, result)
        npz = result.pop("_npz", None)
        if giveup:
            result["decision"] = "left_unchanged"
            result["reason"] = giveup
            return result

        # Content-fit gate — BEFORE the already_in_sync branch: a wrong-episode
        # subtitle can measure as perfectly aligned and must not early-out past it.
        if args.content_gate and npz is not None and out_cues:
            stage("checking the text belongs to this video")
            why = content_fit_verdict(npz, out_cues, result)
            if why:
                result["decision"] = "rejected_wrong_content"
                result["reason"] = why
                return result

        # With a lead configured the question is not "is it aligned?" but "is it
        # already at the target?", and that needs a tighter tolerance:
        # NOOP_THRESHOLD (0.5) is wider than the lead itself, so reusing it here
        # would call every untouched file fine and the lead would never land.
        if LEAD_OUT:
            gap, limit = result.get("net_shift", 0.0), LEAD_TOLERANCE
        else:
            gap, limit = result["max_shift"], NOOP_THRESHOLD
        if gap < limit:
            result["decision"] = "already_in_sync"
            return result

        result["decision"] = "synced"
        if not args.dry_run:
            stage("writing the new timing")
            # mkstemp (not a fixed .tmp name) so concurrent runs on subtitles
            # sharing a basename can never race on the temp file.
            fd, tmp = tempfile.mkstemp(
                dir=os.path.dirname(args.subtitle), suffix=".srt.tmp")
            os.close(fd)
            try:
                write_srt(out_cues, tmp)
                # mkstemp creates 0600; keep the original file's mode
                os.chmod(tmp, os.stat(args.subtitle).st_mode & 0o777)
                bak = backup_path(args.subtitle)
                if bak and not os.path.exists(bak):   # keep the FIRST backup = true original
                    os.makedirs(os.path.dirname(bak), exist_ok=True)
                    shutil.copy2(args.subtitle, bak)
                    result["backup"] = bak
                os.replace(tmp, args.subtitle)
                result["changed"] = True
            finally:
                if os.path.exists(tmp):
                    os.remove(tmp)
        return result
    finally:
        shutil.rmtree(workdir, ignore_errors=True)


def cmd_verify(args):
    """Independent check of a subtitle that is already in the library: one
    whole-file measurement against the video's own audio (never the embedded
    track - that is what the sync may have used), plus text checks."""
    out = {"subtitle": os.path.basename(args.subtitle), "problems": []}
    workdir = tempfile.mkdtemp(prefix="pocket-verify.")
    try:
        stage("reading the audio")
        npz = speech_npz(args.video, workdir, centre=False)
        stage("measuring the whole file")
        ok, off, _ = ffsubsync_run(npz, args.subtitle,
                                   os.path.join(workdir, "verify.srt"),
                                   maxoff=MAX_OFFSET)
        out["target"] = -args.lead
        out["tolerance"] = args.tolerance
        if not ok or off is None:
            out["offset"] = None
            out["problems"].append("unmeasurable against the audio")
        else:
            out["offset"] = round(off, 2)
            if abs(off + args.lead) > args.tolerance:
                out["problems"].append(f"off by {off + args.lead:+.2f}s")
        raw = open(args.subtitle, encoding="utf-8-sig", errors="replace").read()
        if args.check_cyrillic and re.search(r"[Ѐ-ӿ]", raw):
            out["problems"].append("Cyrillic script")
        if args.check_mt:
            why = looks_machine_translated(args.subtitle)
            if why:
                out["problems"].append("machine-translation smell")
        tracks = []
        try:
            st = os.stat(args.video)
            prefix = f"{os.path.basename(args.video)}.{st.st_size}.track"
            for f in sorted(os.listdir(CACHE_DIR)):
                if f.startswith(prefix) and f.endswith(".refcheck.json"):
                    m = re.search(r"\.track(\d+)\.refcheck\.json$", f)
                    v = json.load(open(os.path.join(CACHE_DIR, f)))
                    tracks.append({"track": int(m.group(1)), "ok": bool(v.get("ok")),
                                   "offset": v.get("offset"), "why": v.get("why")})
        except (OSError, ValueError):
            pass
        out["tracks"] = tracks
        out["ok"] = not out["problems"]
        return out
    finally:
        shutil.rmtree(workdir, ignore_errors=True)


def cmd_doctor(_args):
    info = {"python": sys.version.split()[0], "cache_dir": CACHE_DIR}
    for name, cmd in (("ffmpeg", ["ffmpeg", "-version"]),
                      ("ffprobe", ["ffprobe", "-version"]),
                      ("alass", [ALASS, "--version"])):
        try:
            r = run(cmd, timeout=20)
            line = (r.stdout or r.stderr).strip().splitlines()[0]
            info[name] = line
        except (OSError, IndexError, subprocess.TimeoutExpired) as e:
            info[name] = None
            info.setdefault("errors", []).append(f"{name}: {e}")
    code = _ffs_prelude() + (
        "import ffsubsync, numpy, webrtcvad\n"
        "print(ffsubsync.__version__, numpy.__version__)\n")
    r = run([sys.executable, "-c", code], timeout=60)
    parts = r.stdout.split()
    if r.returncode == 0 and len(parts) == 2:
        info["ffsubsync"], info["numpy"] = parts
    else:
        info["ffsubsync"] = None
        info.setdefault("errors", []).append("python deps: " + (r.stderr or "")[-300:])
    info["ok"] = not info.get("errors")
    return info


def main():
    global CACHE_DIR, ALASS, FFSUBSYNC_LIBS
    ap = argparse.ArgumentParser(prog="pocket_sync")
    ap.add_argument("--cache-dir", default=CACHE_DIR)
    ap.add_argument("--alass", default=ALASS)
    ap.add_argument("--ffsubsync-libs", default=FFSUBSYNC_LIBS)
    ap.add_argument("--nice", type=int, default=0,
                    help="lower the priority (audio decoding is heavy; playback must win)")
    sub = ap.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("sync", help="measure and (if needed) re-time one subtitle")
    s.add_argument("video")
    s.add_argument("subtitle")
    s.add_argument("--backup-path", help="where the first original is kept")
    s.add_argument("--lead", type=float, default=0.0,
                   help="seconds the cues land AFTER the voice (0 = audio timing)")
    s.add_argument("--centre-ref", action="store_true")
    s.add_argument("--polish", action="store_true")
    s.add_argument("--ref-residual", action="store_true")
    s.add_argument("--latinize", action="store_true",
                   help="convert Serbian Cyrillic to Latin first")
    s.add_argument("--mt-gate", action="store_true",
                   help="reject machine translations (fresh downloads)")
    s.add_argument("--no-content-gate", dest="content_gate", action="store_false")
    s.add_argument("--pinned", action="store_true",
                   help="only normalise the script; never touch timing")
    s.add_argument("--dry-run", action="store_true")

    v = sub.add_parser("verify", help="independent audio check of one subtitle")
    v.add_argument("video")
    v.add_argument("subtitle")
    v.add_argument("--lead", type=float, default=0.0)
    v.add_argument("--tolerance", type=float, default=0.6)
    v.add_argument("--check-cyrillic", action="store_true")
    v.add_argument("--check-mt", action="store_true")

    sub.add_parser("doctor", help="report toolchain versions")

    args = ap.parse_args()
    CACHE_DIR, ALASS, FFSUBSYNC_LIBS = args.cache_dir, args.alass, args.ffsubsync_libs
    if args.nice:
        try:
            os.nice(args.nice)
        except OSError:
            pass

    handler = {"sync": cmd_sync, "verify": cmd_verify, "doctor": cmd_doctor}[args.cmd]
    try:
        out = handler(args)
        code = 0
    except Exception as e:                                   # noqa: BLE001
        out = {"decision": "error", "reason": f"{type(e).__name__}: {e}"}
        if args.cmd != "doctor" and getattr(args, "subtitle", None):
            out["subtitle"] = os.path.basename(args.subtitle)
        code = 1
    print(json.dumps(out), flush=True)
    return code


if __name__ == "__main__":
    sys.exit(main())
