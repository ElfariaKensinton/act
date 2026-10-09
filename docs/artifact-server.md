# Standalone artifact server

This fork adds a small entrypoint around the existing artifact service implementation.

Build and run from the repository root:

```sh
go run ./cmd/artifact-server --dir ./artifacts --addr 127.0.0.1 --port 8088
```

The server listens on localhost by default. To make it reachable by a GitHub-hosted or remote self-hosted runner, put it behind an HTTPS reverse proxy or a private overlay network and configure the runner's `ACTIONS_ARTIFACTS_RESULTS_URL_OVERRIDE` to the public base URL, including the trailing slash.

**Compatibility and security:** the existing implementation is a protocol starting point, not yet certified against `actions/upload-artifact@v7`. Test uploads and downloads with non-sensitive data before relying on it. Do not expose the service directly to the public internet: add network access controls and HTTPS, and review the implementation's signed-URL/authentication behavior before storing private artifacts. The runner override redirects the Results API endpoint only; it does not move GitHub's run metadata or artifact listing UI to this server.
