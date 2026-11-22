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
- Notebook continues to serialize requests for the same Notebook account.

## Limitations

- Page leasing and selection are now shared, but Playground's WAA-specific
  runtime implementation is intentionally retained to avoid regressions.
- Notebook native streaming and full multi-page concurrency are not included.
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
