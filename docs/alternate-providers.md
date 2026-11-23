# Antigravity OAuth and Gemini Web Chat bridges

These providers are optional additions to the existing AI Studio and NotebookLM
routes. Both share the regular `PROXY_API_KEY` authentication and support the
OpenAI-compatible `POST /v1/chat/completions` and `GET /v1/models` endpoints.
The original model routing remains unchanged.

## Antigravity OAuth / Cloud Code Assist

You can complete Google consent through the gateway's authenticated
management API, or import a legitimate Antigravity OAuth credential file
from Antigravity / CLIProxyAPI. Do not commit access or refresh tokens.

For interactive consent, configure a **new** file path and the matching OAuth
client ID (and secret, if required). Set
`ADMIN_AUTH_ENABLED=true` and an admin password before enabling
the management endpoint on any interface reachable by other users.
Restart the gateway, sign into its admin UI, then send:

```http
POST /api/bridge/antigravity/oauth/start
```

The management API responds with an `authorization_url`. Open it in your
regular browser, approve the Google scopes, and the localhost callback at
`http://localhost:51121/oauth-callback` exchanges the one-time code
using PKCE. The flow lasts at most five minutes and stores credentials
atomically in your configured file with owner-only permissions where
supported. Register the exact redirect URI with the OAuth client.
The callback only listens on 127.0.0.1. Remote deployments need a browser
on the server or an intentionally configured SSH loopback tunnel.

For safety the endpoint **will not overwrite** an imported credential file
owned by another app; use a new path for gateway-managed logins.

Set the following options in `.env`, then restart the gateway:

```ini
ANTIGRAVITY_OAUTH_FILE=C:/Users/you/.cli-proxy-api/antigravity-account.json
ANTIGRAVITY_OAUTH_CLIENT_ID=your-authorized-oauth-client-id
ANTIGRAVITY_OAUTH_CLIENT_SECRET=
ANTIGRAVITY_PROJECT_ID=
ANTIGRAVITY_MODELS=gemini-3.8-flash-high
```

The client credentials must match the OAuth refresh token's issuer.
The client secret may be omitted for an OAuth client that doesn't require it.
For imported files without a client ID field, configure the matching
`ANTIGRAVITY_OAUTH_CLIENT_ID` explicitly; the proxy does not substitute a
different application's OAuth identity.
`ANTIGRAVITY_PROJECT_ID` may be left blank to try the upstream
`loadCodeAssist` discovery RPC. The model list must be populated deliberately,
because upstream model availability changes and no availability is assumed.

The bridge reads nested `token.access_token` and `token.refresh_token` files
as well as flat JSON OAuth credentials. Access tokens are refreshed in memory
through Google's token endpoint under a mutex. It does **not** alter an
external credential file owned by another process, or start a browser.
For an imported credential file, authorization is handled by its original
issuer. For a new gateway-owned file, use the admin OAuth start endpoint.
If a token is revoked, authenticate again with the same client.

Example:

```json
{"model":"antigravity:gemini-3.8-flash-high","messages":[{"role":"user","content":"Hello"}]}
```

## Gemini Web Chat through an existing Playground account

Start the Playground generation service with `WAA_BACKEND=spidermonkey`
(the default). Each enabled Playground account appears as

`gemini-web:<playground-account-id>:default`

in `GET /v1/models`. Use that exact model ID when calling the Chat API. The
bridge borrows the existing account's authenticated HTTP client and synchronized
Google cookies, so it does **not** create a new Chrome/Camoufox process, export
Google cookies, or require a new browser profile.

The upstream web UI's build label and CSRF token are read anew from
`https://gemini.google.com/app` for each generation, then the
`StreamGenerate` RPC is invoked with the same Google session. The browser
account must actually have Gemini Web access. A Playground login does not
guarantee that Gemini Web grants access; an expired cookie yields
`auth_required` rather than falling back silently to anonymous mode.

Only the account's **default model** is supported in this initial adapter.
No unsupported per-model selection is advertised. For other web UI models,
dynamic `listModels` parsing and model-selection headers need to be added.
The Camoufox backend does not currently implement a separate Gemini Web tab
pool and returns `backend_unsupported`; switch to SpiderMonkey for this
feature.

## Compatibility and operational limits

- Both bridges currently support text-only chat. Tool calling, file uploads,
  media, reasoning flags and multiple choices (`n > 1`) are explicitly rejected.
- `stream=true` returns the final answer as a single SSE content chunk, not
  token-by-token upstream streaming. Failures are reported before SSE headers.
- Token usage fields are labelled **estimated**, based on text length.
- The Gemini Web adapter reconstructs the submitted role-marked context in
  one prompt. It does not keep a hidden server-side browser conversation per
  API client, so separate callers cannot leak history to each other.
- Google can change private web/RPC protocols at any time. An empty response
  produces an upstream error rather than an empty successful completion.
- `API_KEY_ACCOUNTS` restricts Gemini Web model access to the corresponding
  Playground source account. Antigravity currently uses one independently
  configured OAuth credential and is available to valid gateway API keys.
- Credential files should be OS-readable only by the user running the gateway.
  Avoid enabling request body logging if sensitive prompts are submitted.

To test an account, call `GET /v1/models`, choose the namespaced model ID,
then send a small text-only Chat Completion with your regular
`Authorization: Bearer <PROXY_API_KEY>` header.

## Credential and deployment safety

OAuth consent does not bypass Google account permissions or Antigravity's
service restrictions. The gateway does not store the user's Google password.
Protect `ANTIGRAVITY_OAUTH_FILE`, do not expose the OAuth callback to
untrusted remote networks, and use a reverse proxy with TLS plus gateway
API-key authentication when accessing the API remotely.

This experimental integration uses upstream private protocols. It should be
verified with a real authorized account before relying on it in production.
