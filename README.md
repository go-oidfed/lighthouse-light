# LightHouse-light

A config-driven, **no-database** deployment of the [LightHouse](https://github.com/go-oidfed/lighthouse)
federation entity, meant for testing, proof-of-concepts, and small
deployments:

- **No admin API** — everything is configured in advance.
- **Filesystem persistence** — all federation content and runtime state lives
  as plain JSON files in a data directory (`data_dir`). Operators provision
  content by writing those files (they ARE the content config) and back them
  up like any other files.
- **KMS fixed to the filesystem** — signing keys persist in the configured
  key dir; private keys never appear in data files (`kms` / `pk_backend` /
  `pkcs11` options do not exist).

Config holds operational settings and general (entity-level) settings; those
are applied at every start and are **never written into the data files** (the
KV store is purely in-memory — there is no `kv.json`). Content lives only
in the data files. Removing something from its config removes its effect.
The data files contain **no numeric ids** — IDs are derived from entity IDs
at load time.

## Quickstart (2 minutes)

```sh
docker build -t oidfed/lighthouse-light .

mkdir -p data
docker run --rm -p 7672:7672 \
  -v "$PWD/config-example.yaml:/config.yaml:ro" \
  -v "$PWD/data:/data" \
  oidfed/lighthouse-light /config.yaml

curl -fsS http://localhost:7672/.well-known/openid-federation | cut -d. -f2 | base64 -d
```

Without Docker: `go build -o lighthouse-light ./cmd/lighthouse-light` (Go
1.27+), then `./lighthouse-light <config.yaml>`.

Provision content (subordinates, trust anchors, …) by writing
`data/subordinates.json`, `data/trust_anchors.json`, … — the formats are in
[docs/data-directory.md](docs/data-directory.md).

## Documentation

| Page                                             | Contents                                                      |
|--------------------------------------------------|---------------------------------------------------------------|
| [docs/index.md](docs/index.md)                   | Overview, quickstart, how an instance is configured           |
| [docs/config.md](docs/config.md)                 | Full config-file reference (every field, default, effect)     |
| [docs/data-directory.md](docs/data-directory.md) | The 9 JSON files: purpose, format, provisioning              |
| [docs/signing.md](docs/signing.md)               | Keys, algorithms, rotation, persistence                       |
| [docs/endpoints.md](docs/endpoints.md)           | All 13 endpoint types, auth, path/URL rules                   |
| [docs/deployment.md](docs/deployment.md)         | Docker single/batch/compose, reverse-proxy TLS, operations    |

The docs are built with **mkdocs-material** — the same stack and styling as
the upstream LightHouse docs — and published to
<https://go-oidfed.github.io/lighthouse-light/> by the
`.github/workflows/docs.yml` GitHub Actions workflow (push to `docs/`,
`mkdocs.yml`, or the workflow itself; deploy on push/workflow_dispatch).

Build and preview locally:

```sh
python3 -m venv .venv && .venv/bin/pip install mkdocs mkdocs-material
.venv/bin/mkdocs serve      # http://127.0.0.1:8000/
.venv/bin/mkdocs build      # static site → ./site
```

One-time setup for Pages deployment: repository **Settings → Pages → Source:
GitHub Actions**.

## Caveats

- **No admin API** — configure via the YAML file and the data-dir files.
- **Content lives in the data files** — a fresh instance has empty content
  until you provision files.
- **Config-owned settings are never written to disk** — the KV store is
  in-memory; they are applied from the config at every start.
- **In-file runtime state** — issued trust marks, replay entries, enrolled
  subordinates, refreshed trust-anchor keys etc. are plain JSON in `data_dir`;
  back them up with the config and the key dir.
- **TLS** — native TLS via `server.tls` (serves on 443), or a reverse proxy
  (Caddy/Traefik/nginx) in front.
- **One instance per data dir** — LightHouse is single-process by design;
  batch deployments run many instances, each with its own data dir.

## License

MIT (same as LightHouse).