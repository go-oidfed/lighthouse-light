# Deployment

## Build variants

### Docker (recommended)

Build from the `go-oidfed/` parent directory — the module uses a local
replacement for the sibling `../lighthouse`, so the build context must
include it:

```sh
cd /path/to/go-oidfed
docker build -f lighthouse-light/Dockerfile -t oidfed/lighthouse-light .
```

- Multi-stage build (`golang:1.27-bookworm` → `debian:stable-slim` with
  `ca-certificates` + `curl`).
- Because the context is the parent directory, the **workspace root's**
  `go-oidfed/.dockerignore` controls what enters the build; keep the
  `lighthouse-light/docker-data` / `lighthouse-light/data` entries so
  root-owned bind-mount dirs do not break the build (a root `.dockerignore`
  ships with this repo; prune the entries if the layout changes).
- `ENTRYPOINT ["/entrypoint.sh"]` forwards all arguments, so the config file
  is the container command: `docker run oidfed/lighthouse-light /config.yaml`.

### Go toolchain

```sh
cd lighthouse-light
go build -o lighthouse-light ./cmd/lighthouse-light
./lighthouse-light config.yaml
```

Requires Go 1.27+ (the toolchain auto-switch selects 1.27.0) and a CGO-capable
toolchain (the imported server links SQLite at build time even though no
database is configured).

## Single instance

```sh
mkdir -p data
docker run -d --name ll \
  -p 7672:7672 \
  -v "$PWD/lighthouse-light/config-example.yaml:/config.yaml:ro" \
  -v "$PWD/data:/data" \
  -v "$PWD/keys:/keys" \
  oidfed/lighthouse-light /config.yaml
```

`config-example.yaml` sets `data_dir: /data` and `key_dir: /data/keys`;
mount the data dir so state survives container replacement. Provision content
by writing the `data/*.json` files (or let the instance materialize the empty
files on first boot, then stop and edit — see
[data-directory.md](data-directory.md)).

## Batch of instances

One instance per data dir (LightHouse is single-process by design). Either
`docker compose` or a loop:

```sh
# compose (see docker-compose.yaml — ships configs/ia.yaml as the demo
# instance on :7673; its data dir lives under ./docker-data)
docker compose up -d

# or a loop over configs — each instance needs its own data dir
for cfg in configs/*.yaml; do
  name=$(basename "$cfg" .yaml)
  docker run -d --name "ll-$name" \
    -v "$PWD/$cfg:/config.yaml:ro" \
    -v "$PWD/docker-data/$name:/data" \
    -v "$PWD/docker-data/$name/keys:/data/keys" \
    oidfed/lighthouse-light /config.yaml
done
```

`configs/ia.yaml` is the one shipped config (entity ID
`http://localhost:7673`); it serves `:7672` inside the container, mapped to
the host port above. For a multi-instance mesh, copy the config per entity,
set a distinct `entity_id` (and host port), and provision
`authority_hints.json` / `trust_anchors.json` in each data dir — there is no
second baked-in config file. Verify:

```sh
curl -fsS http://localhost:7673/.well-known/openid-federation | cut -d. -f2 | base64 -d
```

## TLS

Two options:

1. **Native TLS** — `server.tls` in the config: `enabled: true` plus `cert`
   and `key` (PEM paths; relative to the config file). The server then
   listens on **port 443**; with `redirect_http: true` it also serves port 80
   and redirects to HTTPS. Manage the certificate yourself (certbot, a CA,
   or your own PKI). Binding ports 80/443 needs root or `cap_net_bind_service`.

   ```yaml
   server:
     tls:
       enabled: true
       redirect_http: true
       cert: /data/tls/fullchain.pem
       key: /data/tls/privkey.pem
   ```

   `entity_id` must match the certificate's hostname.

   !!! note "Redirect location is derived from the request `Host` header"
       The HTTP→HTTPS redirect keeps whatever host (`Host` header, including
       port) the client sent, e.g. a request to `http://localhost:8080/…`
       redirects to `https://localhost:8080/…`. This is correct for standard
       deployments where the public origin has no port (hostname-only
       `Host`, e.g. `https://lighthouse.example.org`). When a non-443 host
       port is mapped to container port 80/443 (or a reverse proxy in front
       forwards a ported `Host`), the redirect points at the wrong port —
       terminate TLS at the proxy in that case.

2. **Reverse proxy** — recommended when you want managed certificates or
   HTTPS on a non-443 port: terminate TLS at Caddy, Traefik, or nginx and
   keep the instance on plain HTTP:

   ```
   # Caddyfile
   lighthouse.example.org {
     reverse_proxy 127.0.0.1:7672
   }
   ```

In both cases set `entity_id` to the public `https://` URL so signed
statements, published endpoint URLs and client `aud` claims line up. Keep
`server.ip_listen` on `127.0.0.1` (or an internal network) when a proxy is in
front; `trusted_proxies` / `forwarded_ip_header` matter only for logging with
a proxy.

## Operations

### Boot lifecycle

- Startup: load + validate config → open the 9 data files (corrupt or
  legacy-format files abort, naming the file) → generate/load keys (auto
  generation on first boot) → create the entity → register endpoints →
  start listening.
- Shutdown: `SIGTERM` / `SIGINT` → stop background loops (key rotation,
  proactive resolver, eligibility/issued-mark caches, entity collectors) →
  graceful close (observed clean shutdown ≈ 0.1 s, exit 0). Send one signal;
  do not immediately SIGKILL.

### State & backup

Backup = `data_dir/` + signing `key_dir/`. Both are plain files; rsync/tar
while stopped is safe because all writes are tmp+rename. Restore = restore
both into place, start.

### Content change deployment

Edit the `data_dir/*.json` files, restart. Content removal is removal —
nothing re-seeds content from the config. General settings change via
`config.yaml`; restart.

### Troubleshooting

- **`store <x>: failed to parse <file>`** at boot — corrupt JSON; fix the
  file or delete it (a fresh empty file materializes; re-provision content).
- **`store <x>: file uses the legacy numeric-ID format ...`** — the file was
  written by LightHouse-light v0.1; re-provision
  ([data-directory.md](data-directory.md)).
- **`store subordinates: ...: status cannot be active without keys`** — an
  active subordinate row has no `jwks`; add keys or set `status: pending`.
- **`trust anchor "..." is not defined in trust_anchors.json`** — an endpoint
  references an anchor that is not provisioned; add it to the file.
- **404 on fetch** — the subject is not in `subordinates.json` or its status
  is blocked.
- **400 `invalid_request` on an auth endpoint** — no `client_assertion` /
  `client_assertion_type`, or the assertion's `iss`/`sub`/`aud` don't line up;
  see the auth section in [endpoints.md](endpoints.md).
- **`kid` changes after restart** — expected after rotation; the auto
  generated key itself is stable across restarts.

### Running the container as a non-root user

The image runs as root by default, so files the server writes into a
bind-mounted data dir (`data_dir/*.json`, `key_dir/`) are root-owned — a
non-root operator then needs `sudo` (or `docker exec … chown`) to edit them
while the instance is stopped. To run as your own user, add `user` to the
compose service and pre-chown the bind mounts:

```yaml
services:
  ia:
    # ...
    user: "${UID}:${GID}"
```

With `user` set, the data dir must be owned by that uid/gid before start
(`chown -R "$UID:$GID" docker-data/ia`). Two notes:

- **`docker compose build` does not need root** — it runs in the Docker
  daemon; the `user:` directive only affects the running container, not the
  build. Steps inside the Dockerfile that need root (e.g. `apt-get`) run as
  root within the build regardless.
- The one privileged operation that legitimately needs an extra capability is
  binding container port 443 for native TLS from a non-root process: use
  `cap_add: [NET_BIND_SERVICE]`, or terminate TLS at a reverse proxy.

### Logging

`logging.level` + `logging.format` (json/console). `debug` logs request
protocol/header/body decisions; `info` logs startup lines, endpoint
registration and the configured trust-anchor count; `warn`/`error` log
validation, signing and resolution failures.