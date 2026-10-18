---
name: local-transcribe
description: Use when a task involves audio or video — transcription, subtitles, meeting notes, podcast or episode summaries, searching inside recordings. Runs Whisper (large-v3-turbo) on the Mac instead of the cloud.
---

Step 0: `local_status` / mode rules as in `local-offload`. `local_capabilities` must report
`services.whisper: true`; if not, tell the user and stop — don't send bulk audio to a cloud service
without asking, in either mode.

1. `local_transcribe(path, language?)` per file — any audio or video format on the NAS. No LLM profile
   needed. The NAS only stream-copies the audio track out of videos; decoding and Whisper run on the
   Mac, much faster than real time. Pass `language` (ru, en, …) when known, otherwise auto-detect.
2. You get `.txt` (with `[hh:mm:ss]` marks), `.srt` and `.json` paths plus a short preview. Don't read
   whole long transcripts yourself.
3. Post-processing (summary, notes, action items, search) → `local-research` workflow: `fast` with
   `fast/summarize` / `fast/extract-json` per chunk of the .txt, `brain/synthesize` to merge.
   Subtitles: the `.srt` is ready as is.
4. Prefer: spot-check a few timestamped quotes against the .txt. Strict: report as unverified.
5. `local_feedback` with the verdict.

Whisper needs ~2 GB next to the loaded model — fine with every profile except possibly `brain`/`heavy`;
if memory is tight, transcribe everything first, then load the big profile.
