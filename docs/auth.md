# Login und OAuth

Der Server ist selbst OAuth-2.1-Autorisierungsserver für den MCP-Endpunkt. Die konfigurierten Identity Provider (IdP) dienen nur dem Login. Claude, ChatGPT und andere MCP-Clients registrieren sich per Dynamic Client Registration (RFC 7591) und holen Tokens bei diesem Server.

## Endpunkte

| Pfad | Zweck |
|---|---|
| `/.well-known/oauth-protected-resource[/mcp]` | Resource-Metadaten (RFC 9728), im `WWW-Authenticate` der 401-Antwort verlinkt |
| `/.well-known/oauth-authorization-server` | AS-Metadaten (RFC 8414), inkl. `authorization_response_iss_parameter_supported` |
| `POST /oauth/register` | Client-Registrierung (nur öffentliche Clients, begrenzt auf erlaubte Redirect-URIs) |
| `GET /oauth/authorize` | PKCE (S256) Pflicht; `resource` muss auf diesen Server zeigen; Auswahlseite bei mehreren IdPs |
| `GET /oauth/callback/{provider}` | Rücksprung vom IdP |
| `POST /oauth/token` | Code gegen Token tauschen, Refresh mit Rotation |
| `POST /oauth/revoke` | Token widerrufen (RFC 7009) |

Eigenschaften, die der Server durchsetzt:

- Tokens sind opak, in der Datenbank nur als SHA-256-Hash; Access 1 h, Refresh 30 Tage (`auth.*_ttl`).
- Tokens sind an die Ressource (`<base_url>/mcp`) gebunden (RFC 8707); fremde Audiences werden mit 401 abgewiesen.
- Refresh-Tokens rotieren. Wird ein bereits benutzes Refresh-Token vorgelegt, widerruft der Server alle Tokens dieses Nutzers für diesen Client.
- Autorisierungscodes gelten 2 Minuten und genau einmal.
- Der Nutzer wird bei jeder Anfrage live geprüft: Deaktivierte Konten verlieren sofort den Zugriff, auch mit gültigem Token.
- Der Server fordert beim IdP selbst PKCE und eine Nonce an.

## Redirect-URIs der Clients

`auth.allowed_redirect_uris` listet die Rückkehradressen, die sich registrieren dürfen; ein `*` am Ende steht für beliebigen Rest. Mitgeliefert (Stand 11/2026, vor Livegang gegen die Anbieter-Dokumentation prüfen):

- Claude: `https://claude.ai/api/mcp/auth_callback` (Anthropic kündigt einen Wechsel auf `claude.com` an, beide sind eingetragen)
- ChatGPT: `https://chatgpt.com/connector_platform_oauth_redirect`, `https://chatgpt.com/connector/oauth/*`
- Lokale Clients wie Claude Code nutzen `http://localhost:<port>/…`; das erlaubt `auth.allow_loopback_redirects` (Standard `true`).

## Identity Provider einrichten

Beim IdP als Redirect-URI eintragen: `<base_url>/oauth/callback/<provider-id>` (die `id` aus der Config).

**Google** (`type: google`): OAuth-Client „Webanwendung“ in der Google Cloud Console (einmalig durch den Betreiber). Zugelassen werden Konten, deren ID-Token einen `hd`-Claim aus `allowed_domains` trägt (Google Workspace), sowie einzeln in `allowed_emails` gelistete Adressen (z. B. private Gmail-Konten, die keinen `hd`-Claim haben). Mindestens eines von beiden ist Pflicht; `email_verified` muss immer `true` sein. `allowed_emails` gilt auch für `type: oidc`, nicht für Entra.

**Microsoft Entra ID** (`type: entra`): App-Registrierung, Redirect-Typ „Web“. `allowed_tenants` ist Pflicht. Bei genau einem Tenant wird dessen Discovery-Dokument genutzt, bei mehreren das `organizations`-Dokument mit manueller Issuer-Prüfung (`iss` muss zu `tid` passen). Die E-Mail wird aus `email`, sonst `upn`/`preferred_username` gelesen. Entra liefert kein `email_verified`; die Bindung an den Tenant ersetzt es. *Noch nicht gegen einen echten Entra-Tenant getestet.*

**Generisches OIDC** (`type: oidc`): `issuer`, `allowed_domains` und ein verifiziertes E-Mail-Feld sind Pflicht. Sendet der IdP kein `email_verified`, kann `allow_unverified_email: true` gesetzt werden – nur für IdPs, die keine ungeprüften Adressen ausstellen.

## Offene Punkte

- Client ID Metadata Documents (von der Spezifikation bevorzugt) sind nicht umgesetzt; Dynamic Client Registration funktioniert mit Claude und ChatGPT.
- Gruppen aus dem ID-Token werden gelesen, aber noch nicht synchronisiert (Phase 2).
- Der periodische Aufräumjob (`Cleanup`) wird mit den Hintergrundjobs in Schritt 9 eingehängt.
