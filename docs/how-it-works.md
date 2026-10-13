# How the engine decides

`engine/pocket_sync.py sync VIDEO SUBTITLE` prints one JSON verdict. In order:

1. **Script.** Serbian Cyrillic becomes Latin when the language is listed (the first original is backed up).
2. **Pinned?** Then nothing else happens.
3. **Machine translation** (fresh downloads in the listed languages): no translator credit, ≥ 21% fragment cues, average line < 26 characters and no dialogue dashes → `rejected_machine_translation`.
4. **Speech reference.** The audio track is decoded once with ffmpeg and passed through WebRTC voice detection (`--vad webrtc` is deliberate: ffsubsync's default would use an embedded *subtitle* track as "speech" and every later check would be circular). On 5.1 audio a centre-channel reference is also built when the full mix explains the dialogue poorly, and the better one is kept. A reference with speech in under 5% of the runtime is refused — measuring silence produces confident nonsense.
5. **Embedded English tracks** are extracted and measured against the audio, whole file *and* last 30% (a track from another cut can agree overall and drift by the end). The best track that agrees within 1.5 s becomes the reference and the subtitle is aligned to it with **alass**, which follows any number of inserted or removed scenes.
6. **Otherwise, the audio.** Whole-file alignment (±15 s, widened to ±40 s — never wider, spurious peaks appear). The file is split at black-frame act breaks; each act is measured with a narrow and a wide search; a large offset is accepted only if a neighbouring act agrees; outliers are pulled back toward the whole-file answer (no act may stray > 30 s, an insertion must persist to the end, cap-edge hits are discarded, no cue may leave the video). A polish pass re-measures small groups of cues for displaced blocks.
7. **Content fit.** After alignment the cues must cover the detected speech: at least 45% (unless the video's own trusted track also scores poorly — then the audio is the problem) and at least 60% of what that track achieves. Fails → `rejected_wrong_content`.
8. **Lead.** The reading lead is added last. If every cue is already within 0.15 s of its target (0.5 s without a lead) → `already_in_sync`, file untouched. Otherwise the new timing is written atomically (`synced`) and the first original is kept.

Anything that cannot be measured → `left_unchanged` with the reason; the file is never modified.

Every measurement is cached per video (name + size), so a second language of the same episode, a re-check or a verification takes seconds instead of a full audio decode.

`verify` re-measures a file once against the raw audio (never the embedded track) and reports the whole-file offset, script and machine-translation smell, and the cached embedded-track verdicts.
