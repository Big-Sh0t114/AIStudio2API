# Antigravity + Gemini Web (experimental)

This branch **replaces**, rather than extends, the withdrawn draft PR #15. Neither provider changes existing AI Studio, NotebookLM, Build or Playground routing. The model namespaces are intentionally separate.

## Antigravity

Adapted from [Mag1cFall/Antigravity2API](https://github.com/Mag1cFall/Antigravity2API). The provider uses Cloud Code Assist `loadCodeAssist`, `fetchAvailableModels` and `streamGenerateContent`, instead of advertising guessed model names.

Add to `.env`:

```dotenv
ADMIN_AUTH_ENABLED=true
ANTIGRAVITY_OAUTH_FILE=runtime/antigravity/oauth.json
ANTIGRAVITY_CLIENT_ID=<Google desktop OAuth client ID>
ANTIGRAVITY_CLIENT_SECRET=<OAuth client secret if issued>
# Optional if discovery fails for your account:
ANTIGRAVITY_PROJECT_ID=
```

The OAuth client ID must belong to a Google desktop client authorized to use `http://localhost:51121/oauth-callback`. To initiate authentication after enabling admin auth, send `POST /api/alternate/antigravity/oauth/start` through the authenticated admin interface, then open the returned `authorization_url` locally. The callback listener binds only to `127.0.0.1:51121`, uses PKCE and state, expires after five minutes, and saves privately owned credential files with mode 0600. Existing credential JSON files may also be imported, but credentials owned by another application will not be overwritten.

The code does not bundle third-party OAuth client secrets. Client ID and secret must be supplied by the operator, or tokens imported from their existing desktop OAuth account.

Models appear as `antigravity:<model-id>`. Their availability, provider and thinking-tier identities come from each account's live `fetchAvailableModels` response. Current implementation uses one local OAuth account. Text generation only; no tool calls or multimodal input.

## Gemini Web

Model discovery and generation follow [Mag1cFall/Gemini-Web2API](https://github.com/Mag1cFall/Gemini-Web2API) including the `otAQ7b` model catalog, hash/mode/variant fields, and the `processSession` WebChannel. **This is not the old Bard StreamGenerate endpoint.**

Requires `WAA_BACKEND=spidermonkey` and existing Playground accounts signed into Gemini Web. It uses the resident Playground worker's Go HTTP client, Google cookie store, proxy and lease. There is no independent Chrome process or new account browser. Login to AI Studio does not guarantee Gemini Web access.

Models appear as `gemini-web:<playground-account-id>:<official-model-id>` and `gemini-web:<playground-account-id>:default`; the ID may contain an email address. Available models are read per account and cached for five minutes, with disabled/locked entries omitted. The caller's API key account bindings apply to these shared source accounts. Model discovery uses at most four concurrent borrowed accounts.

Supported chat model IDs also accept a thinking suffix such as `gemini-web:you@gmail.com:gemini-4-argon@think=high` when the source account offers that tier. The values are `low`, `medium`, and `high`; model policies determine available tiers.

Only text conversations are currently implemented. API `stream=true` emits a completed SSE chunk for compatibility, **not** token-by-token streaming. Conversation history is flattened into role-labeled text for now; session persistence, multimedia and official tools remain out of scope. The Gemini Web `processSession` protocol is an unofficial website RPC subject to change. Account access and availability must be tested with a real Google session.

## API examples

```bash
curl -H "Authorization: Bearer YOUR_PROXY_API_KEY" http://127.0.0.1:8007/v1/models

curl -H "Authorization: Bearer YOUR_PROXY_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemini-web:your@gmail.com:gemini-4-argon","messages":[{"role":"user","content":"Hello"}]}' \
  http://127.0.0.1:8007/v1/chat/completions
```

The API returns explicit errors for unsupported tools and multimodal requests. Token counts on these two backends are estimates, not upstream billing data.

## Verification required before merging

1. `go test ./internal/alternate ./internal/api ./internal/app ./internal/aistudio` and `go vet ./...` on the repository's declared Go toolchain.
2. Windows build via the existing repository Actions pipeline.
3. Real Antigravity OAuth loopback and refresh (including forced refresh on HTTP 401), SSE continuation hops and finish reasons.
4. Real Gemini Web cookies, model discovery, WebChannel response and account switching.
5. Security review of admin OAuth authentication and per-key account restrictions.

This is a draft integration, not a verified production proxy.

## Branch validation and Windows build

The branch contains `.github/workflows/alternate-validate.yml`. On a normal developer push to `feat/report-antigravity-gemini-web-v2`, it is configured to run alternate unit tests, compile all internal packages with frontend assets, and cross-compile a Windows desktop EXE. The EXE is uploaded as the `aistudio2api-alternate-windows-amd64` workflow artifact only after the checks succeed.

If GitHub Actions does not start after connector-authored commits, an authorized developer can create a normal push from a local checkout of this feature branch, without merging it:

```bash
git checkout feat/report-antigravity-gemini-web-v2
git commit --allow-empty -m "ci: trigger alternate validation"
git push origin feat/report-antigravity-gemini-web-v2
```

A workflow file being present is **not** evidence that checks have run or an executable has been built. Review the workflow logs and test real Google sessions before marking PR #16 ready for review.
