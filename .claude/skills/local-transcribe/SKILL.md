---
name: local-transcribe
description: Use when a task involves audio or video — transcription, subtitles, meeting notes, podcast or episode summaries, searching inside recordings. Runs Whisper on the Mac instead of the cloud. (Service planned; check availability first.)
---

Step 0: `local_status` / mode rules as in `local-offload`.

1. `local_capabilities` → `services.whisper` must be `true`. If not: tell the user it isn't set up yet and
   stop. Don't send bulk audio to a cloud service without asking — in either mode.
2. Extract audio on the NAS (`ffmpeg -i in -ac 1 -ar 16000 out.wav`), split long files into chunks.
3. Transcribe via the whisper service; keep timestamps.
4. Post-processing (summary, notes, subtitles) → `local-research` workflow with `fast`/`brain`.
5. Read only the final summary; prefer: spot-check a few timestamped quotes.

Whisper and an LLM profile compete for memory: transcribe everything as one batch first, then load the
LLM profile for post-processing.
