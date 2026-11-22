# Gemini Notebook Provider (experimental)

Unofficial Go-native integration of ideas and private NotebookLM RPC details from
[open-notebooklm](https://github.com/mkshaonexe/open-notebooklm) (MIT). Google can change the
web protocol, usage limits or login requirements without warning. Use only on your
own Google accounts, and review Google's service terms.

## Scope

- Separate Chrome profile per Notebook account under `runtime/notebook/profiles/<id>`.
- Add / sign in / reauthenticate / refresh / remove Notebook accounts from the
  **Accounts → Gemini Notebook** panel. Sign-in requires Chrome on the machine
  running AIStudio2API and an interactive GUI.
- Add Notebook-specific model IDs to OpenAI `GET /v1/models` as
  `notebook:<account-id>:<notebook-id>`; other model names are unchanged.
- Text-only, **non-streaming** OpenAI `POST /v1/chat/completions`.
- Forward full OpenAI user/assistant history; omit Notebook's shared
  `conversationId` to avoid leaking one client's chat history to another.
- Handle expired login, malformed RPC responses and quota errors, including
  `Retry-After`, per-account cooldown and quota-window display.
- Continue using the existing API key for public endpoints. Notebook account
  access observes API key/account-email bindings where configured.
- Show Notebook usage separately from AI Studio token statistics, with no
  fabricated token counts.

## Quick start

1. Start the application as usual; Chrome/Chromium must be installed.
2. Open Accounts → Gemini Notebook, add an account and sign in using the
   newly opened Chrome window on the **server machine**.
3. Click **Refresh status**; select a notebook with at least one ready source.
4. Use the model identifier shown in the notebook list with your regular API key.

```bash
curl http://127.0.0.1:2048/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"notebook:ACCOUNT_ID:NOTEBOOK_ID","stream":false,"messages":[{"role":"user","content":"Summarize the sources"}]}'
```

Optionally set `NOTEBOOK_CHROME_PATH` (or `CHROME_PATH`) to the Chrome executable.

## Limitations and security

This is not a general-purpose Gemini API. Notebook responses are grounded in
uploaded sources, and the provider currently rejects nontext messages, tool
calling, `n != 1`, and `stream=true`. System/developer instructions are
passed as *ordinary text*, not a privileged system channel. Only one request
per account executes at once.

Chrome profiles hold private session credentials; protect `runtime/notebook/`.
Do not expose Chrome debugging ports beyond localhost. For remote/headless servers
you must solve interactive login separately; no email/password flow is available.

**The feature has not been verified end to end against a signed-in Google account.**
Tests cover local persistence, model parsing, RPC error decoding and API input
conversion. The Google private RPC may be rotated at any time.

## Next milestones

1. Native incremental `GenerateFreeFormStreamed` → OpenAI SSE.
2. Adding Notebook sources, processing status, uploads and cleanup.
3. Deep Research task start/status/cancel, artifacts and persistent bindings.

## License

Original Notebook RPC behavior adapted from
[mkshaonexe/open-notebooklm](https://github.com/mkshaonexe/open-notebooklm),
Copyright (c) 2026 MK Shaon; MIT. See `docs/LICENSE.open-notebooklm`.
AIStudio2API Copyright (c) 2026 Mag1cFall; MIT. Retain both notices.
