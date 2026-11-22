# Shared Playground/Notebook page lifecycle (experimental)

The native Go integration uses **one Camoufox process per Playground account**
for both AI Studio and Notebook. Notebook pages are background tabs of that
process. The protocol adapters remain separate: WAA for Playground and
NotebookLM's internal RPC for Notebook.

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
2. The shared page pool creates a background Notebook tab on demand.
3. Subsequent requests reuse a checked-out Notebook tab if its origin is valid.
4. At the end of the request, the page lease is released immediately.
5. One Notebook tab per worker remains idle for up to **120 seconds**.
6. On timeout, invalid origin, Worker replacement, or service shutdown, the
   page is closed and cannot be reused.
7. No Worker lifecycle or account startup lock is held while that tab is idle.

The existing standalone Chrome Notebook mode remains available, but **no
silent Chrome fallback** is performed for linked Camoufox accounts.

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
