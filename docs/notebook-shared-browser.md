# Shared Playground/Notebook page lifecycle (experimental)

The native Go integration supports two distinct existing Playground runtimes
with the **same Google account**. Both preserve their existing worker's
lifecycle and proxy; NotebookLM keeps separate RPC and quota handling.

- **Camoufox:** use a background Notebook tab in the existing Firefox browser.
- **SpiderMonkey:** reuse the Playground worker's native Go HTTP client,
  synchronized Google cookies, proxy and user agent to call Notebook RPC
  directly. SpiderMonkey is a WAA JavaScript VM, not a tab-capable browser.
  No extra browser, Chrome profile or Notebook tab is required.

## Unified page management

`internal/pagepool` provides origin-neutral page allocation primitives:

- **PickOldestFree** is used by the existing Playground page selector
  (`aistudio/page_tabs.go`) and the generic page pool.
- **NeedsSpare** preserves Playground's established prewarming threshold for
  concurrent page requests.
- **Pool + Lease** control request-scoped checkout, idle caching, expiration,
  shutdown invalidation, and context cancellation. Notebook uses this shared
  implementation instead of a separate `notebookTabCache`.
- Playground continues to integrate its specialized `AccountLease`,
  WAA `workerRuntime`, proof handling and page replacement logic; these
  cannot be treated as Notebook pages without changing the upstream protocol.
- Both services use the same `AccountPool` activity accounting for Worker
  eviction. Notebook operations increment the shared browser operation count
  without affecting AI Studio model quota or protected requests.

## Notebook lifecycle

1. First request for a linked account borrows the existing Playground Worker.
2. For Camoufox, the shared page pool creates a background Notebook tab on
   demand. Subsequent requests borrow the same tab when valid.
3. Camoufox page leases are released after each request, and idle tabs close
   after **120 seconds**. Worker shutdown clears the cache.
4. For SpiderMonkey, the Notebook adapter fetches the Notebook bootstrap
   over the existing authenticated Go HTTP transport, caches its transient
   CSRF/page metadata for **2 minutes** in memory, and issues HTTP RPCs.
   Google cookies remain owned and updated by the underlying Playground
   worker. There is no Notebook page to destroy.
5. Both paths release the shared worker lifecycle lease after the request.
   No worker startup lock is held during Notebook idle time.

The existing standalone Chrome Notebook mode remains available, but **no
silent Chrome fallback** is performed for linked accounts of either backend.

## Conversations and metadata

- Each Chat Completions request sends only its own OpenAI `messages`.
- No Notebook conversation ID or server-side chat history is deliberately
  shared across clients by AIStudio2API.
- Notebook ready-source IDs and titles are cached for **60 seconds** per
  Notebook ID, with invalidation on refresh and upstream request errors.
- The source catalog does not include prompts, chat history, or answers.
- Notebook linked to Playground supports two concurrent calls per Google source, even across independent Notebook documents. Each request has an isolated borrowed page or SpiderMonkey HTTP adapter.

## Limitations

- Page leasing and selection are now shared, but Playground's WAA-specific
  runtime implementation is intentionally retained to avoid regressions.
- Notebook native streaming is not included. Linked accounts now allow up to two simultaneous page/HTTP requests; independent standalone Chrome accounts remain serialized.
- The Google Notebook private RPC may change; real Google sign-in and actual
  latency/memory improvements still need end-to-end validation on Windows.
- Worker pool capacity and separate service quotas remain controlled by
  their existing managers.

## SpiderMonkey authentication limitations

- Direct Notebook requests use `https://notebook.google.com/` and its
  `/_/LabsTailwindUi/data/` RPC endpoints only. The adapter does not
  execute arbitrary remote JavaScript or forward cookies to another origin.
- If the saved Google account cookies do not authenticate Notebook, the
  account returns `auth_required` instead of creating a new Chrome process.
  This can happen when Google uses device-bound or service-specific login.
- The Notebook page bootstrap is parsed from the actual HTML into short-lived
  memory, never saved to `accounts.json` or exposed by the management API.
- CSRF 403 errors invalidate the cached bootstrap and retry once; 429
  continues to use the existing independent Notebook cooldown.
- Real Google login and Notebook RPC compatibility remain to be tested on
  a signed-in Windows installation. Passing CI does not establish upstream
  functionality.

## Idempotent Playground account binding

- `POST /api/notebook/accounts` with `source_account_id` returns the
  **existing** Notebook account when that exact Playground source has already
  been bound: HTTP 200 with `{"created":false,"account":...}`. No new Notebook
  ID, model namespace, usage counter, cooldown or login status is generated.
- The first binding returns HTTP 201 with `{"created":true,"account":...}`.
- Repeated login failures do not require creating another Notebook record.
  The Account page reloads records even if the login request fails.
- Backend source-ID deduplication is atomic across concurrent create requests,
  including when Playground source discovery takes time.
- A saved association can be reused while Playground source discovery is
  temporarily unavailable. A **new** association still requires validation.
- Existing duplicate records are **not automatically merged or deleted**.
  They may be referenced by external Notebook model IDs and hold separate
  historical local counters. The UI flags these legacy duplicates for manual
  review; delete one only after checking which account ID clients use.
- Equal Google email addresses across distinct Playground source IDs produce
  a warning, not a forced merge. Google quotas are still tied to the same
  actual Google identity even when local records differ.
- Independent Chrome Notebook accounts are still created separately and are
  not deduplicated by email.

## Bounded Notebook concurrency

Linked Playground accounts use an independent, source-keyed Notebook gate:

- **2 active Notebook requests** per `source_account_id`.
- **10 queued requests** per source at most; the 11th waiting request receives
  `429 notebook_queue_full` instead of occupying unlimited goroutines.
- Queue wait is bounded to **30 seconds**; requests exceeding it return
  `504 notebook_queue_timeout`. An HTTP client cancel stops waiting.
- Google HTTP/RPC 429 places all Notebook account aliases mapped to the same
  Playground source in a **shared cooldown**, preserving the existing 10-minute
  fallback when upstream returns no reset time.
- Each concurrent Notebook request has its own temporary `browser` wrapper,
  source metadata map and web request; no chat history or live browser tab is
  shared between requests.
- Account status, cooldown and request counters use short mutex sections
  instead of locking through the entire generation.
- Camoufox provisions up to **2 Notebook background tabs**, each idle for
  120 seconds before closing. SpiderMonkey shares the original Go HTTP
  client, with request-local token/bootstrap state and no browser tabs.
- Playground Worker lifecycle transitions continue to take exclusive
  `startupMu.Lock`; Notebook requests borrow a shared
  `startupMu.RLock` until their page lease is returned. This prevents
  shutdown/restart from replacing the Worker while either Notebook call runs.
- Playground request slots and AI Studio quota routing remain independent.
- Standalone Chrome Notebook sessions remain **single-flight**, since their
  CDP page must not be used simultaneously by two unrelated requests.

The scheduler does **not** claim Google NotebookLM supports two simultaneous
generations for every account. The upstream can still serialize, return 429
or demand separate authentication. Validate with real multi-request traffic.

## Notebook request logs and token usage

- Notebook Chat Completions now records its public routing model ID,
  `notebook:<account-id>:<notebook-id>`, in the shared request logger and
  usage ledger, together with the Notebook account alias and channel.
- The underlying model used by Google NotebookLM **is not disclosed** by the
  private RPC. Do not interpret the public routing ID as a Google Gemini model
  fingerprint or an upstream `provider_model`.
- NotebookLM does not return reliable input/output/reasoning token usage.
  To avoid blank request statistics, successful text-only requests now return
  **estimated** prompt/completion/total token counts using a deterministic
  character-based heuristic (CJK characters counted individually; other
  characters approximately 4 per token).
- These are explicitly marked `usage.estimated=true`,
  `usage.estimation_method="text_only_heuristic"`, and
  `X-Token-Usage-Estimated: true`. Request logs show an estimate label.
  The estimation excludes Notebook source documents, internal retrieval,
  hidden prompts, server reasoning, and any provider-side token accounting.
- Do **not** use Notebook token estimates to determine billing, quotas or
  upstream throughput. Google quota/cooldown events remain authoritative.
- Existing request logs are not retroactively changed.

## Configurable Notebook parallelism

The Gemini Notebook account page now exposes a single persistent
**Notebook requests per Playground source** setting, ranging from **1 to 16**
(default **4**). This is a local scheduling limit and not an official
Google NotebookLM per-account concurrency entitlement.

- The setting is loaded from and saved to `runtime/notebook/accounts.json`
  using the top-level `concurrency` field. Existing installations without
  that field start at 4. Notebook accounts, model IDs, cooldowns and usage
  statistics remain unchanged when adjusting concurrency.
- The authenticated control API exposes `GET /api/notebook/settings` and
  `PUT /api/notebook/settings` with `{"concurrency":4}`. Invalid values,
  including zero and numbers over 16, return HTTP 400.
- Changes affect the local source-scoped queue **immediately**. Increasing
  the setting wakes waiting requests; reducing it never interrupts work
  already running. New requests wait for active requests to drop below
  the configured limit.
- Camoufox's Notebook-only page pool supports resizing between 1 and 16.
  It is resized on the next Notebook page borrow; any excess idle tabs close
  immediately when resizing, or expire normally after 120 seconds while
  otherwise unused. Active Notebook tabs remain leased until their
  requests finish, while Playground's WAA tabs are unaffected.
- SpiderMonkey has no Notebook tabs. It uses the same configurable
  source-level concurrent RPC gate, with independent request-local bootstrap
  state and the existing shared authenticated Go HTTP client.
- Up to 10 queued Notebook requests and a 30-second queue timeout remain
  independent of the active concurrency setting.
- Standalone Chrome Notebook accounts intentionally remain single-flight,
  because a single existing CDP browser page cannot safely process multiple
  independent chats simultaneously.

## Troubleshooting `sources_not_ready` (HTTP 400)

This error is raised before the generation RPC when `rLM1Ne` returns no
usable ready source IDs. The error now includes ingestion counts:

- `total=0`: the selected Notebook has no source rows. Verify the Notebook
  model ID and inspect the official NotebookLM Sources panel.
- `processing>0`: sources are still processing; wait and refresh.
- `failed>0`: sources failed ingestion; retry or replace the source.
- `unknown>0`: source status is unrecognized by the adapter.
- `invalid_ids>0`: source ID envelope is not recognized; debug the source
  row's structure without sharing private content.

The adapter handles plain, nested and Drive-backed IDs and older rows without
a status slot. Explicitly pending, failed, deleted, and unknown-status sources
are not sent to generation. The 60-second ready-source cache remains in place.
