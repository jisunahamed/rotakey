# Groq audio through Rotakey

Use the Rotakey base URL, your Rotakey gateway key and the **public alias** saved
on your provider. The upstream Groq key stays on the server.

| Public endpoint | Groq models | Input | Output |
| --- | --- | --- | --- |
| `POST /v1/audio/transcriptions` | `whisper-large-v3`, `whisper-large-v3-turbo` | Multipart audio file or audio URL | JSON, verbose JSON or text |
| `POST /v1/audio/translations` | `whisper-large-v3` | Multipart audio file or audio URL | English translation |
| `POST /v1/audio/speech` | `canopylabs/orpheus-v1-english`, `canopylabs/orpheus-arabic-saudi` | JSON text and voice | WAV bytes |

Whisper is an audio recognition model, not a Chat Completions model. It cannot
answer ordinary `/v1/messages` prompts. Audio models are tagged in OpenAI model
discovery and excluded from the Anthropic chat-model picker. Existing supported
Groq chat models continue to use the existing Chat/Responses/Messages paths.

## Transcribe an uploaded recording

```sh
curl "$ROTAKEY_URL/v1/audio/transcriptions" \
  -H "Authorization: Bearer $ROTAKEY_KEY" \
  -F "model=whisper-large-v3-turbo" \
  -F "file=@recording.wav" \
  -F "language=bn" \
  -F "response_format=verbose_json" \
  -F "timestamp_granularities[]=word" \
  -F "timestamp_granularities[]=segment"
```

Do not manually set Content-Type for a multipart upload; curl/the SDK generates
the required boundary. Supported formats include WAV, MP3, FLAC, M4A, MP4, OGG
and WebM. Language is optional. Repeated timestamp fields, prompt, temperature,
file name, file headers and audio bytes are retained when Rotakey selects a key
or retries. Only the public model alias is replaced with the upstream model ID.

## Translate speech to English

```sh
curl "$ROTAKEY_URL/v1/audio/translations" \
  -H "Authorization: Bearer $ROTAKEY_KEY" \
  -F "model=whisper-large-v3" \
  -F "file=@recording.wav" \
  -F "response_format=text"
```

Turbo does not support translation; Rotakey rejects that combination before
charging an upstream request.

## Generate speech

```sh
curl "$ROTAKEY_URL/v1/audio/speech" \
  -H "Authorization: Bearer $ROTAKEY_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"orpheus-v1-english","input":"Hello from Rotakey.","voice":"troy","response_format":"wav"}' \
  --output speech.wav
```

Groq currently documents a 200-character input limit for Orpheus and WAV output.
The Arabic model needs one of its Arabic voices. Provider permissions and model
terms still apply; an entitlement rejection is not a gateway transport failure.

## Operations

- Provider base URL: `https://api.groq.com/openai/v1`.
- Existing Whisper/Orpheus routes are reclassified by migration; their aliases,
  enabled state, credentials and running limits are retained.
- New catalog imports and model checks recognize these audio families. A check
  sends a small WAV or short speech request rather than a chat completion.
- The model editor exposes an audio API type for other compatible model IDs.
- Requests share provider FIFO admission, key rotation, RPM limits, cancellation
  and timeout rules. Audio bytes/duration are not estimated as text tokens.
  Audio-specific duration/character billing and Groq ASH/ASD limits are not yet
  represented in Rotakey's text-token meters. Upstream rate limits still apply.
- `MAX_AUDIO_BYTES` defaults to 27,262,976 bytes (26 MiB, including multipart
  overhead). Groq documents a 25 MB attachment limit; use its `url` field for
  larger supported files. Configure your external proxy to permit audio uploads.
- Successful provider responses retain their bytes, status and content type,
  including plain text and binary audio. No chat normalization or automatic AI
  repair is applied to audio bodies. A missing token count does not cause a retry.

Official references:
[Speech to text](https://console.groq.com/docs/speech-to-text),
[Text to speech](https://console.groq.com/docs/text-to-speech),
[Orpheus parameters](https://console.groq.com/docs/text-to-speech/orpheus).
