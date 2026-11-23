"""Unit tests for the pure parts of the engine (no ffmpeg, no audio)."""
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pocket_sync as ps  # noqa: E402


def srt(cues):
    return "\n\n".join(f"{i+1}\n{ps.fmt_ts(s)} --> {ps.fmt_ts(e)}\n{t}" for i, (s, e, t) in enumerate(cues)) + "\n"


class TestText(unittest.TestCase):
    def test_cyr2lat(self):
        self.assertEqual(ps.cyr2lat("Љубав и ЊЕГОШ, Џон"), "Ljubav i NJEGOŠ, Džon")
        self.assertEqual(ps.cyr2lat("Ђорђе ће"), "Đorđe će")

    def test_parse_write_roundtrip(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "a.srt")
            cues = [(1.0, 2.5, "Hello"), (3.25, 4.0, "two\nlines")]
            open(p, "w").write(srt(cues))
            got = ps.parse_srt(p)
            self.assertEqual([(round(s, 3), round(e, 3), t) for s, e, t in got], cues)
            out = os.path.join(d, "b.srt")
            ps.write_srt(got, out)
            self.assertEqual(ps.parse_srt(out), got)

    def test_latinize_keeps_backup(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "x.sr.srt")
            open(p, "w", encoding="utf-8").write(srt([(1, 2, "Здраво")]))
            ps.BACKUP_PATH = os.path.join(d, "bak", "x.sr.srt")
            self.assertTrue(ps.latinize_if_cyrillic(p))
            self.assertIn("Zdravo", open(p, encoding="utf-8").read())
            self.assertIn("Здраво", open(ps.BACKUP_PATH, encoding="utf-8").read())
            self.assertFalse(ps.latinize_if_cyrillic(p))

    def test_machine_translation_heuristic(self):
        with tempfile.TemporaryDirectory() as d:
            mt = os.path.join(d, "mt.srt")
            open(mt, "w").write(srt([(i * 3, i * 3 + 2, "Svaki." if i % 3 == 0 else "Idemo sada kuci") for i in range(60)]))
            self.assertIsNotNone(ps.looks_machine_translated(mt))
            human = os.path.join(d, "human.srt")
            open(human, "w").write(srt([(i * 3, i * 3 + 2, "-Gdje si bio cijelu noc?\n-Kod brata, rekao sam ti.") for i in range(59)]
                                       + [(200, 202, "Preveo: neko")]))
            self.assertIsNone(ps.looks_machine_translated(human))


class TestSegments(unittest.TestCase):
    def test_small_segments_merge(self):
        cues = [(float(i), i + 0.5, "x") for i in range(40)]
        segs = ps.segment_cues(cues, [5.0, 30.0])
        self.assertEqual(sum(len(s) for s in segs), 40)
        self.assertTrue(all(len(s) >= ps.MIN_CUES_PER_SEG for s in segs[:-1]))


try:
    import numpy as np
except ImportError:      # the engine image has it; a bare host may not
    np = None


@unittest.skipUnless(np is not None, "numpy not installed")
class TestContentGate(unittest.TestCase):
    """The gate must reject subtitles that do not lock onto the speech, and
    must NOT reject a correct subtitle just because the detector heard little
    of the dialogue (loud score, no embedded text track: the reported loop)."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        rng = np.random.default_rng(7)
        # 20 minutes of dialogue lines, 1.5-3.5 s long, 3-9 s apart
        self.lines, t = [], 5.0
        while t < 1190:
            d = rng.uniform(1.5, 3.5)
            self.lines.append((t, t + d, "line"))
            t += d + rng.uniform(3, 9)
        speech = np.zeros(120000, dtype=bool)
        for s, e, _ in self.lines:          # the detector hears only 40% of each line
            speech[int(s * 100):int((s + (e - s) * 0.4) * 100)] = True
        self.npz = os.path.join(self.tmp.name, "v.npz")
        np.savez(self.npz, speech=speech)
        # another episode: same rhythm, unrelated times
        self.other, t = [], 7.0
        while t < 1190:
            d = rng.uniform(1.5, 3.5)
            self.other.append((t, t + d, "line"))
            t += d + rng.uniform(3, 9)

    def tearDown(self):
        self.tmp.cleanup()

    def test_correct_subtitle_on_loud_video_passes(self):
        r = {}
        self.assertIsNone(ps.content_fit_verdict(self.npz, self.lines, r))
        self.assertLess(r["content_fit"], ps.FIT_FLOOR)       # the old floor would have rejected it
        self.assertGreater(r["content_sharpness"], ps.FIT_SHARP_MIN)

    def test_other_episode_is_rejected(self):
        r = {}
        self.assertIsNotNone(ps.content_fit_verdict(self.npz, self.other, r))
        self.assertLess(r["content_lift"], ps.FIT_LIFT_MIN)

    def test_healthy_ref_judges_by_ratio_not_floor(self):
        r = {"ref_fit": 0.52}   # Verity: 0.44 against a 0.52 track
        self.assertIsNone(ps.content_fit_verdict(self.npz, self.lines, r))

    def test_ratio_still_rejects(self):
        r = {"ref_fit": 0.95}
        self.assertIn("embedded track", ps.content_fit_verdict(self.npz, self.lines, r))


if __name__ == "__main__":
    unittest.main()
