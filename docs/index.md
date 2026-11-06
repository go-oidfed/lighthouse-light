# LightHouse-light

LightHouse-light is a no-database packaging of the
[LightHouse](https://go-oidfed.github.io/lighthouse/main/) Federation Entity,
meant for testing, proof-of-concepts, and small deployments.

Compared to LightHouse, it differs in three ways:

- **No admin API.** Everything is configured in advance — there is no mutating
  HTTP interface.
- **Plain-file persistence.** All state lives as JSON files in a data
  directory, not in a database. You provision content by writing those files
  (before the first start, or while the instance is stopped), and you can
  inspect or back them up like any other files.
- **Filesystem signing keys.** Keys are stored in the configured key file or
  key directory. There is no HSM/PKCS#11 and no database-backed key storage.

The result is the same LightHouse server, but simpler to operate: one YAML
file, a directory of JSON files, and a reverse proxy in front for TLS.

## Quickstart (2 minutes)

```sh
mkdir -p data
docker run --rm -p 7672:7672 \
  -v "$PWD/lighthouse-light/config-example.yaml:/config.yaml:ro" \
  -v "$PWD/data:/data" \
  oidfed/lighthouse-light /config.yaml

curl -fsS http://localhost:7672/.well-known/openid-federation | cut -d. -f2 | base64 -d
```

Without Docker: `go build -o lighthouse-light ./cmd/lighthouse-light` (Go
1.27+), then `./lighthouse-light <config.yaml>`.

The instance boots with an **empty data dir** — provision subordinates, trust
anchors, trust mark specs, authority hints etc. by writing the files described
in [data-directory.md](data-directory.md), then restart.

## How an instance is configured

There are exactly three places operators touch:

| What | Where |
|---|---|
| Operational settings (server listen address, signing, endpoint paths/auth, log level) | `config.yaml` |
| General (entity-level) federation settings (lifetimes, entity metadata, metadata policies, constraints, general claims) | `config.yaml` |
| Federation content (subordinates, trust anchors, authority hints, trust mark specs/subjects, published trust marks, issuers/owners, extra entity claims) | `data_dir/*.json` — the data files ARE the content config |
| Signing keys | `signing.filesystem.key_file` / `key_dir` (private keys only ever live here) |

Two properties follow from this split:

- **Removing something removes it.** Config settings are applied whole from
  the config file at every start and are never written into the data files;
  rows you delete from a data file are gone at the next start. There is no
  "sync from config" step that could re-create them.
- **Runtime state survives restarts.** Enrolled subordinates, issued trust
  mark instances, requested subjects, and client-assertion replay entries are
  plain JSON in the data dir. They are written at runtime, persist across
  restarts, and are never overwritten by the config.

The data files contain **no numeric ids and no counters**. IDs are derived
automatically from entity IDs when the files are loaded, so you never write or
read them, and references (e.g. an issued trust mark pointing at its subject)
stay valid across restarts. Files written with the old numeric-ID format are
rejected with a clear error — re-provision them (see
[data-directory.md](data-directory.md)).

## The three "where things live"

| Concern | Where | See |
| --- | --- | --- |
| Configuration / general settings | `config.yaml` (path: CLI argument, `$LH_CONFIG_FILE`, or auto-discovery) | [config.md](config.md) |
| Federation content + runtime state | `data_dir/*.json` — 9 files | [data-directory.md](data-directory.md) |
| Signing keys | `signing.filesystem.key_file` / `key_dir` | [signing.md](signing.md) |

## Documentation map

| Topic | Page |
| --- | --- |
| The full config-file reference (every field, default, valid values) | [config.md](config.md) |
| The 9 data-dir files, their formats, and scriptable provisioning | [data-directory.md](data-directory.md) |
| Keys, algorithms, rotation, persistence | [signing.md](signing.md) |
| The 13 endpoint types and endpoint authentication | [endpoints.md](endpoints.md) |
| Docker single/batch/compose, reverse-proxy TLS, operations | [deployment.md](deployment.md) |

## Feature coverage

Everything LightHouse supports is configurable here:

- the full federated endpoint set: fetch, list, resolve, trust mark (status /
  list / request / issuance), historical keys, enroll (+entity checker),
  enroll_request, entity collection, **jwks_update**, **jwks_update_trigger**
- authority hints with **jwks_sync** (push / trigger / none)
- trust mark specs with **eligibility_config** and **cache_ttl**
- general (entity-level) and per-subordinate metadata, metadata policies,
  constraints, additional claims
- trust anchors, published trust marks, trust mark issuers/owners, trust mark
  subjects
- **native TLS** (`server.tls`, incl. HTTP→HTTPS redirect)
- **automatic trust-anchor JWKS refresh** (`enable_jwks_update` +
  `key_poll_interval` in `trust_anchors.json`)
- entity configuration lifetime, statement lifetime, signing algorithm / RSA
  key length / key rotation / automatic key rollover

Not available: the admin API, database backends (SQLite), and key backends
other than filesystem.

## Caveats

- **No admin API** — configure via the YAML file and the data-dir files.
- **Content lives in the data files** — a fresh instance has empty content
  until you provision files. The snapshots in
  [data-directory.md](data-directory.md) show the formats.
- **In-file runtime state** — issued trust marks, replay entries, enrolled
  subordinates etc. are plain JSON in `data_dir`; back them up together with
  the config and the key dir.
- **TLS** — native TLS via `server.tls` (serves on 443); a reverse proxy
  (Caddy/Traefik/nginx) remains a good option for managed certificates, and
  is required to reach an instance on a non-443 host port.
- **One instance per data dir** — LightHouse is single-process by design;
  batch deployments run many instances, each with its own data dir.