# JavDB mirror lookup

The JavDB movie provider queries the backend mirror first by default. The
origin JavDB site is used only as a fallback.

## Behavior

1. Query the mirror (`MIRROR_BASE_URL`) with a per-request
   `MIRROR_TIMEOUT` (default `8s`).
2. On mirror failure, no data, or an incomplete record, fall back to the
   JavDB origin site (mirrors are never sent origin cookies or auth; only
   origin requests may use the CookieCloud session).
3. If the origin site responds with HTTP 403 or redirects to the login page
   (302 login wall), MetaTube synchronizes the browser session from the
   configured CookieCloud server and retries that origin request exactly
   once.

## Configuration

```sh
# Defaults shown; all optional:
MT_MOVIE_PROVIDER_JAVDB__MIRROR_ENABLED=true
MT_MOVIE_PROVIDER_JAVDB__MIRROR_BASE_URL=https://jdforrepam.com/api
MT_MOVIE_PROVIDER_JAVDB__MIRROR_TIMEOUT=8s
```

The existing CookieCloud variables (`MT_MOVIE_PROVIDER_JAVDB__COOKIECLOUD_*`,
`MT_ACTOR_PROVIDER_JAVDBACTOR__COOKIECLOUD_*`) are unchanged and still
required for authenticated origin access. Never put real credentials in
documentation or compose files; pass them via untracked environment.

## Scope

- `FC2-PPV-<number>` is normalized to `FC2-<number>` for mirror queries only;
  media file naming is untouched.
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
- **Mirror timeout or bad gateway:** raise `MIRROR_TIMEOUT` temporarily; the
  provider falls back to the origin automatically.
- **Roll back to origin-only lookups:** set
  `MT_MOVIE_PROVIDER_JAVDB__MIRROR_ENABLED=false` and restart the server.
  Cached records may still reflect the mirror until refreshed.

## Quick reference

| Variable | Default | Purpose |
|---|---|---|
| `MT_MOVIE_PROVIDER_JAVDB__MIRROR_ENABLED` | `true` | Prefer mirror, fallback to origin |
| `MT_MOVIE_PROVIDER_JAVDB__MIRROR_BASE_URL` | `https://jdforrepam.com/api` | Mirror API base URL |
| `MT_MOVIE_PROVIDER_JAVDB__MIRROR_TIMEOUT` | `8s` | Per-request mirror timeout |
