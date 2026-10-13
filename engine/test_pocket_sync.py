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


if __name__ == "__main__":
    unittest.main()
