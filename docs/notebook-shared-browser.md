# Shared Camoufox browser for Notebook (experimental)

AIStudio2API Notebook can reuse the **same Camoufox process** as an existing
Playground account. Each Notebook operation opens a background tab in that
account's browser and closes it at the end. There is **no extra Chrome process**
for a linked Notebook account.

## Setup

1. Add/sign in to a Playground account using the Camoufox backend.
2. Start the Playground service. A warm Camoufox worker will be reused;
   if none is resident, the ordinary Playground worker startup path is used.
3. In **Accounts > Gemini Notebook**, select **Reuse Playground account**.
4. Create the linked Notebook account. If the Notebook site recognizes the
   same Google session, its notebooks and usage appear automatically.

Existing standalone Notebook accounts still use the dedicated Chrome profile.

## Caveats

- Sharing requires an active Playground service and a **Camoufox**, not a
  SpiderMonkey, worker for the selected account.
- The Notebook tab is temporary; no independent Chrome is launched. Memory
  saving compared to the old implementation is expected but not benchmarked.
- Only the browser process is shared: quotas, cooldowns, notebook history and
  API model IDs remain separate. A Notebook request can still consume memory
  for its temporary page.
- If Google's Notebook page does not accept the Playground cookies, login
  may be required through an independent Chrome account. No automatic
  fallback is performed (to avoid silently starting another browser).
- Google Notebook uses an unofficial internal RPC and may change without notice.
- The Notebook tab is opened with WebDriver BiDi and must not select or
  navigate the Playground tab.
