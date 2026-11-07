# Login and OAuth

The server is itself the OAuth 2.1 authorization server for the MCP endpoint. The configured identity providers (IdP) are used for login only. Claude, ChatGPT and other MCP clients register through Dynamic Client Registration (RFC 7591) and obtain tokens from this server.

## Endpoints

| Path | Purpose |
|---|---|
| `/.well-known/oauth-protected-resource[/mcp]` | Resource metadata (RFC 9728), linked in the `WWW-Authenticate` header of the 401 response |
| `/.well-known/oauth-authorization-server` | AS metadata (RFC 8414), including `authorization_response_iss_parameter_supported` |
| `POST /oauth/register` | Client registration (public clients only, limited to allowed redirect URIs) |
| `GET /oauth/authorize` | PKCE (S256) required; `resource` must point to this server; selection page when there are several IdPs |
| `GET /oauth/callback/{provider}` | Return from the IdP |
| `POST /oauth/token` | Exchange code for token, refresh with rotation |
| `POST /oauth/revoke` | Revoke a token (RFC 7009) |

Properties the server enforces:

- Tokens are opaque and stored in the database only as a SHA-256 hash; access 1 h, refresh 30 days (`auth.*_ttl`).
- Tokens are bound to the resource (`<base_url>/mcp`) (RFC 8707); foreign audiences are rejected with 401.
- Refresh tokens rotate. If a refresh token that has already been used is presented, the server revokes all tokens of that user for that client.
- Authorization codes are valid for 2 minutes and exactly once.
- The user is checked live on every request: deactivated accounts lose access immediately, even with a valid token.
- The server itself requests PKCE and a nonce from the IdP.

## Client redirect URIs

`auth.allowed_redirect_uris` lists the return addresses that may register; a trailing `*` stands for any remainder, and must follow a `/` (`https://host/path/*`): the server refuses `https://host*`, which would also match `https://host.evil.example/`, and compares the host of a wildcard entry exactly. Included by default (as of 11/2026, check against the vendors' documentation before going live):

- Claude: `https://claude.ai/api/mcp/auth_callback` (Anthropic has announced a switch to `claude.com`, both are listed)
- ChatGPT: `https://chatgpt.com/connector_platform_oauth_redirect`, `https://chatgpt.com/connector/oauth/*`
- Local clients such as Claude Code use `http://localhost:<port>/…`; this is allowed by `auth.allow_loopback_redirects` (default `true`).

## Setting up identity providers

Register at the IdP as redirect URI: `<base_url>/oauth/callback/<provider-id>` (the `id` from the config).

**Google** (`type: google`): OAuth client of type "Web application" in the Google Cloud Console (once, by the operator). Accounts are admitted whose ID token carries an `hd` claim from `allowed_domains` (Google Workspace), as well as addresses listed individually in `allowed_emails` (e.g. private Gmail accounts, which have no `hd` claim). At least one of the two is required; `email_verified` must always be `true`. `allowed_emails` also applies to `type: oidc`, not to Entra.

**Microsoft Entra ID** (`type: entra`): app registration, redirect type "Web". `allowed_tenants` is required. With exactly one tenant, its discovery document is used; with several, the `organizations` document with manual issuer validation (`iss` must match `tid`). The e-mail is read from `email`, otherwise from `upn`/`preferred_username`. Entra does not deliver `email_verified`; the binding to the tenant replaces it. *Not yet tested against a real Entra tenant.*

**Generic OIDC** (`type: oidc`): `issuer`, `allowed_domains` and a verified e-mail field are required. If the IdP does not send `email_verified`, `allow_unverified_email: true` can be set, but only for IdPs that do not issue unverified addresses.

## Open points

- Client ID Metadata Documents (preferred by the specification) are not implemented; Dynamic Client Registration works with Claude and ChatGPT.
- Groups from the ID token are read but not yet synchronized (phase 2).

## Accounts, sessions and dev_mode

- **Accounts are not linked by e-mail.** A login with a second provider using an already assigned e-mail is rejected (message to the user, the admin must decide). With `auth.link_accounts_by_email: true` it takes over the existing account, including an instance admin from `bootstrap_admins`; this is only safe if all providers deliver verified e-mails and nobody there can issue arbitrary addresses of the allowed domain. Every link is recorded as `account_linked` in the audit log.
- **Verified e-mail addresses.** An address the identity provider does not vouch for may log in (if the domain or tenant is allowed), but it is **never used** to take over an existing account (`auth.link_accounts_by_email`) and never creates the account of a `bootstrap_admins` address: such a login is refused with a message to contact the administrator, and `login_denied` records it. Google and generic OIDC vouch through `email_verified`. **Entra sends no such claim**, so its addresses count as unverified unless you set `email_trusted: true` on that provider, which says that the tenant has no guests and no user can choose their own mail address. Without it a bootstrap admin cannot log in through Entra; set it, or let the admin log in through a provider that verifies.
- **Absolute session lifetime:** `auth.session_max_age` (default 90 days). After that the user logs in again, even if the connection is used continuously.
- **Your own logins:** `my_sessions` shows the clients you are logged in with, `revoke_my_sessions` ends one or all of them (after losing a device). Instance admins end a user's logins with `admin_revoke_sessions`.
- **`dev_mode`** only runs with a `base_url` on localhost and `instance.listen` on the loopback interface (e.g. `127.0.0.1:8080`), and requires its own `dev_token` (at least 16 characters). It is no longer generated or logged.

## Who may log in (without a restart)

`allowed_domains`, `allowed_emails` and `allowed_tenants` from `config.yaml` form the base. Instance admins extend them at runtime with `admin_allowlist`, no restart needed:

- `action=list` shows the providers and all entries (`config` = from the configuration, not changeable; `admin` = created here, with admin and date).
- `action=add` and `remove` with `kind` = `domain` (e.g. `partner.de`), `email` (single address) or `tenant` (Entra tenant ID as GUID), optionally with `provider`, otherwise for all matching providers. Domains and addresses apply to `google` and `oidc`, tenants to `entra`.
- The database entries only **extend**: what the configuration allows cannot be forbidden this way. With Entra only the tenants grow (a domain list would restrict there). If a second tenant is added to a single one, the check itself switches to the `organizations` document with issuer validation per tenant.
- `add` and `remove` need `confirm=true` after explicitly asking the user, every change is in the audit log (`admin_allowlist_add`, `admin_allowlist_remove`). Public mail domains (gmail.com, outlook.com, gmx.de …) are rejected as a domain; use single addresses for those.
- Removing does not lock existing accounts: use `admin_offboard_user` and `admin_revoke_sessions` for that.
- Rejected logins write `login_denied` to the audit log with provider and reason (domain or tenant, never the e-mail address). No account is created.
- If the database cannot be read during login, only the configuration applies. This can only reject more, never allow more.
