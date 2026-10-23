# Standalone artifact server

This fork adds a small entrypoint around the existing artifact service implementation.

Build and run from the repository root:

```sh
go run ./cmd/artifact-server --dir ./artifacts --addr 127.0.0.1 --port 8088
```

## Automatic expiration

The server saves each upload's `expires_at` deadline beside the archive and
deletes expired artifact directories, including staged upload blocks, on startup
and every minute while running. Deadlines survive restarts. Cleanup waits for
active transfers to finish before removing files.

With the patched runner, official `actions/upload-artifact` steps inherit the
repository's `GITHUB_RETENTION_DAYS` setting when `retention-days` is omitted or
zero. The action sends the resulting expiration to this server. An explicit
workflow `retention-days` value still works and is capped by the official client
at the repository's limit. No additional GitHub API token is needed.

For clients that do not send an expiration, configure a fallback:

```sh
go run ./cmd/artifact-server --dir ./artifacts --retention-days 14
```

`--retention-days` accepts 1–400 days and defaults to 90. It is a fallback, not an
override of deadlines supplied by uploads. Existing artifact directories without
retention metadata expire this many days after their newest file was modified.
While the server is stopped, cleanup is deferred until its next startup. Changes
to repository retention apply to new uploads; saved deadlines are not extended.

## GitHub summary appearance

The runner generates the GitHub job summary; this repository serves the files
and a separate HTML artifact page. GitHub sanitizes job-summary HTML, removing
styles and classes. Its native Artifacts card cannot be reproduced exactly in a
custom job summary by changing either repository. Artifact names in the existing
summary remain download links.

The server listens on localhost by default. To make it reachable by a GitHub-hosted or remote self-hosted runner, put it behind an HTTPS reverse proxy or a private overlay network and configure the runner's `ACTIONS_ARTIFACTS_RESULTS_URL_OVERRIDE` to the public base URL, including the trailing slash.

**Compatibility and security:** the existing implementation is a protocol starting point, not yet certified against `actions/upload-artifact@v7`. Test uploads and downloads with non-sensitive data before relying on it. Do not expose the service directly to the public internet: add network access controls and HTTPS, and review the implementation's signed-URL/authentication behavior before storing private artifacts. The runner override redirects the Results API endpoint only; it does not move GitHub's run metadata or artifact listing UI to this server.

### HTTPS reverse proxy

When running behind a trusted HTTPS reverse proxy, configure the proxy to **overwrite** (not pass through client-supplied) `X-Forwarded-Proto: https` and `X-Forwarded-Host` with the public hostname. The artifact service uses these headers when constructing upload/download URLs. Keep the service bound to localhost or a private network; do not trust forwarded headers from untrusted clients.

The standalone command is only an entry point around the existing `act` artifact routes. Passing its build does not establish compatibility with every `actions/upload-artifact` release. Verify the exact action version with a real upload and download before depending on it.
