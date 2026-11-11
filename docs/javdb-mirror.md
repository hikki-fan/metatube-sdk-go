# JavDB mirror lookup

The JavDB movie provider uses the backend mirror by default. Origin JavDB
scraping is disabled unless `mirror-fallback` is explicitly selected.

## Behavior

1. Query the mirror (`MIRROR_BASE_URL`) with a per-request
   `MIRROR_TIMEOUT` (default `8s`).
2. In the default `mirror-only` mode, mirror errors are returned and empty
   search results stay empty; neither case triggers a JavDB origin request.
3. `mirror-fallback` explicitly enables JavDB scraping after a mirror error or
   missing detail record. In that mode, configured CookieCloud recovery can
   retry an origin request once after a login wall or HTTP 403.
4. Origin-page image recovery follows the same mode. Mirror-only mode does not
   request JavDB when a mirror-provided image URL fails to load.

Mirror requests use a separate HTTP client. They do not receive JavDB cookies
or CookieCloud credentials. When both mirror credentials are configured, the
client logs in through `/v1/sessions` before requesting metadata and sends the
returned token in the mirror's `Authorization` header. No cookies are retained
and redirects are rejected, including redirects to another host.

Without mirror credentials, existing anonymous access remains available; some
records require a logged-in mirror account. Configure both username and
password together; configuring just one is an error. Changing credentials
discards the mirror client, session, and metadata cache. Concurrent requests
share login work. HTTP 401 or an explicit session-expired response triggers at
most one login refresh per request. HTTP 403/429 enters cooldown without
refreshing the session.

The `JavDBActor` provider uses the mirror for actor searches and detail
lookups by ID, returning mirror avatar URLs and available alternate names.
Actor searches return the mirror's candidate list without selecting a fuzzy
match. Set the actor provider's mode to `mirror-fallback` to enable JavDB
scraping after a mirror error.

## Configuration

```sh
# Defaults shown; all optional:
MT_MOVIE_PROVIDER_JAVDB__MIRROR_ENABLED=true
MT_MOVIE_PROVIDER_JAVDB__MIRROR_BASE_URL=https://jdforrepam.com/api
MT_MOVIE_PROVIDER_JAVDB__MIRROR_TIMEOUT=8s
MT_MOVIE_PROVIDER_JAVDB__MIRROR_MODE=mirror-only
# Optional login; set both through protected environment configuration:
# MT_MOVIE_PROVIDER_JAVDB__MIRROR_USERNAME
# MT_MOVIE_PROVIDER_JAVDB__MIRROR_PASSWORD
MT_ACTOR_PROVIDER_JAVDBACTOR__MIRROR_MODE=mirror-only
```

CookieCloud is only needed when origin scraping is enabled. The existing
variables (`MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_*`,
`MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_*`) remain available for that mode.
Never put real credentials in documentation or compose files; pass them via
untracked environment.

## Scope

- `FC2-PPV-<number>` is normalized to `FC2-<number>` for mirror queries only;
  media file naming is untouched.
- Movie provider IDs remain the exact opaque IDs returned by the mirror, with
  provider name `JavDB`; query normalization does not rewrite stored IDs.
- This is a backend mirror only. No source file renaming, no Emby ProviderId
  change, and no changes to AutoFilm download/organization configuration or
  the plugin UI are needed.

## Troubleshooting

- **Metadata looks stale after enabling/disabling the mirror:** existing
  cache may take priority over network results. Force a metadata refresh in
  the client for affected items.
- **Origin requests still fail after CookieCloud sync:** synchronization
  cannot re-login on your behalf. Confirm the browser session can access
  JavDB, then sync again. Check that the CookieCloud UUID/password match and
  that `COOKIECLOUD_REFRESH_INTERVAL` has elapsed.
- **Mirror timeout or bad gateway:** raise `MIRROR_TIMEOUT` temporarily, or
  explicitly enable `MIRROR_MODE=mirror-fallback` if origin access is wanted.
- **Mirror returns a business error for a known record:** configure the mirror
  account username and password. These are separate from CookieCloud settings.
  For HTTP 403/429, wait for cooldown rather than repeatedly logging in.
- **Use origin-only lookups:** set
  `MT_MOVIE_PROVIDER_JAVDB__MIRROR_ENABLED=false` and restart the server.
  Cached records may still reflect the mirror until refreshed.

## Quick reference

| Variable | Default | Purpose |
|---|---|---|
| `MT_MOVIE_PROVIDER_JAVDB__MIRROR_ENABLED` | `true` | Enable mirror lookups |
| `MT_MOVIE_PROVIDER_JAVDB__MIRROR_BASE_URL` | `https://jdforrepam.com/api` | Mirror API base URL |
| `MT_MOVIE_PROVIDER_JAVDB__MIRROR_TIMEOUT` | `8s` | Per-request mirror timeout |
| `MT_MOVIE_PROVIDER_JAVDB__MIRROR_MODE` | `mirror-only` | `mirror-fallback` opts into origin scraping |
| `MT_MOVIE_PROVIDER_JAVDB__MIRROR_USERNAME` | unset | Mirror account username; requires password |
| `MT_MOVIE_PROVIDER_JAVDB__MIRROR_PASSWORD` | unset | Mirror account password; requires username |
| `MT_ACTOR_PROVIDER_JAVDBACTOR__MIRROR_MODE` | `mirror-only` | `mirror-fallback` opts into origin scraping |

Actor mirror credentials use the equivalent
`MT_ACTOR_PROVIDER_JAVDBACTOR__MIRROR_USERNAME` and
`MT_ACTOR_PROVIDER_JAVDBACTOR__MIRROR_PASSWORD` variables.
