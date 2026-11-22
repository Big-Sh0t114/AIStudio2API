# Notebook requests with Playground-style warm Camoufox workers

Notebook shares an existing AIStudio2API Playground account's Camoufox worker
and follows the same **warm-worker, independent-request** principle.
An OpenAI Chat Completion always constructs its context from that request's
`messages`, not from the browser's previous conversation.

## Page lifecycle

- First Notebook operation: reuse an existing Playground Camoufox worker, or
  start one with the normal Playground worker manager. Open the Notebook page in
  a **background tab**, without navigating the AI Studio chat tab.
- Next operation within 120 seconds: reuse the same Notebook tab. A cheap
  same-origin check detects stale/redirected tabs and recreates them if needed.
- After each operation: release the Notebook request lease immediately,
  including the worker's lifecycle lock. **Do not lock the worker during idle.**
- After 120 seconds without a Notebook operation: close the idle tab.
- When a Playground worker shuts down, retires, or is replaced: invalidate its
  cached Notebook tab. A new worker starts with an empty Notebook tab cache.
- One idle Notebook tab maximum per Playground worker. Operations borrowing
  the same worker currently serialize at the account worker lifecycle lock.
- Dedicated Chrome Notebook accounts remain available as a legacy option.

## Request isolation

- Each OpenAI Chat Completions request transmits only its own `messages` and
  creates its own upstream generate request. The browser tab is a transport
  container, not a shared chat history.
- No remote Notebook conversation ID is intentionally persisted or reused.
- Requests for different Notebook IDs share the source account's browser but
  do not inherit each other's client-supplied messages.
- The Notebook `rLM1Ne` source metadata RPC currently runs per request; this
  change reduces page initialization costs but does not cache source metadata.

## Requirements and limitations

1. Start Playground service and select an existing enabled Playground account
   in the Notebook account selector.
2. Shared mode requires a Camoufox worker, not SpiderMonkey.
3. Google might ask for Notebook authentication even when Playground is signed
   in; there is no silent fallback to another Chrome process.
4. Notebook's unofficial private RPC is subject to upstream changes.
5. Latency and RAM savings are expected but **not benchmarked**. The tab can
   consume memory for up to 120 seconds after its last operation.
6. Native Notebook streaming and a multi-tab concurrency pool are not yet
   implemented. Same-account requests remain serialized.
