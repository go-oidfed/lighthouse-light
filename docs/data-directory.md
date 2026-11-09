# The data directory

All federation content and runtime state persists as **plain, hand-editable
JSON files** under `data_dir` (default `/data`). These files are 
**additional configuration files**: subordinates, trust anchors, authority
hints, trust mark specs/subjects and published trust marks are provisioned
here, not in the YAML config. Write the files before the first start, or stop
the instance, edit, and start again — changes are picked up at the next boot.

- **One instance per data dir** — LightHouse is single-process by design and
  the files have no cross-process locking.
- Writes are **atomic** (`<file>.tmp` + rename): a crash never leaves a torn
  file.
- **Corrupt files abort startup**, and the error names the file (e.g.
  `store <name>: failed to parse <file>`). To recover, either **fix the
  JSON** — the file is then loaded normally on the next start — or **delete
  the file**, in which case an empty store is created in its place on the
  next start (and you re-provision its content from scratch).

## The 9 files

There is one file per storage area. Each section below describes what the
file is for and what its JSON looks like. Content files (`subordinates`,
`trust_anchors`, `trust_mark_*`, `authority_hints`,
`published_trust_marks`) you provision by hand; the runtime files
(`jti.json`, `trust_mark_instances.json`) are maintained by the server
(details below).

### `authority_hints.json`

The **superiors** of this instance. The file is a bare array of hint rows;
the entity configuration's `authority_hints` claim publishes only the
`entity_id`s of these rows. Each hint may carry an outbound key-sync
directive (`jwks_sync`); that block is **never published** — it only controls
outbound notifications after key rotation.

```json
[
  {
    "entity_id": "https://superior.example.org",
    "description": "my superior",
    "jwks_sync": { "mode": "push", "jwt_lifetime": 600, "timeout": 20 }
  }
]
```

`jwks_sync` modes (outbound-only, never published):

| Mode      | Behavior                                                                                                                        |
|-----------|---------------------------------------------------------------------------------------------------------------------------------|
| `none`    | (default) no syncing.                                                                                                           |
| `push`    | After a key rotation, POST a signed JWK Set containing the new keys to the superior's `federation_jwks_update_endpoint` (the endpoint and acceptable signing algorithms are resolved from the superior's entity configuration at rotation time). |
| `trigger` | POST this entity's `entity_id` to the superior's `federation_jwks_update_trigger_endpoint`, authenticated with a signed client assertion. |

Options: `jwt_lifetime` (s, default 600), `timeout` (s, default 20), and
`headers` (map).

### `trust_mark_types.json`

The **trust mark types** this instance issues or coordinates, each with its
inline owner and allowed issuers. Used to publish `trust_mark_owners` and
`trust_mark_issuers` in the entity configuration.

```json
[
  {
    "trust_mark_type": "https://example.org/trustmark",
    "description": "Example trust mark type",
    "owner": {
      "entity_id": "https://owner.example.org",
      "description": "The type owner",
      "jwks": { "keys": [ { "kty": "EC", "crv": "P-256", "kid": "o1" } ] }
    },
    "issuers": [
      { "issuer": "https://tmi.example.org", "description": "Allowed issuer" }
    ]
  }
]
```

Each row carries `owner` (`{entity_id, description, jwks}`) and `issuers`
(`[{issuer, description}]`), both optional.

### `trust_anchors.json`

The **trust anchors** used for endpoint auth and chain resolution, as a bare
array. Each row is identified by its entity ID and carries the anchor's public
keys. A row may opt into automatic key refresh.

!!! info "references must resolve"
    Endpoints reference trust anchors by **entity ID only**
    (`auth_trust_anchors`, `allowed_trust_anchors`, `endpoints.auth.trust_anchors`).
    The anchor itself must exist in this file, or startup fails with
    `trust anchor "..." is not defined in trust_anchors.json`.

```json
[
  {
    "entity_id": "https://root.example.org",
    "enable_jwks_update": true,
    "key_poll_interval": 300,
    "jwks": {
      "keys": [
        { "kty": "EC", "crv": "P-256", "kid": "root" }
      ]
    }
  }
]
```

!!! info "automatic key refresh"
    `enable_jwks_update: true` makes the instance poll the anchor's entity
    configuration and keep its stored `jwks` fresh; `key_poll_interval`
    (seconds; 0 = derived from the anchor's configuration lifetime). Refreshed
    keys are written back into this file and **used immediately by endpoint
    auth**; the refresher stops cleanly on shutdown. `jwks` is **required**
    unless you are bootstrapping via the flag — an anchor with the flag but
    no `jwks` boots empty and is filled from the first successful poll.

    Two caveats:

    - The **initial poll runs synchronously at startup**, before the HTTP
      server listens: if the anchor's entity configuration cannot be fetched
      at that moment (unreachable host, transient error), startup **fails**.
      The anchor should be reachable and stable before the instance boots.
    - An anchor whose `entity_id` equals the instance's **own** entity ID is
      **skipped with a warning** (its keys are already authoritative locally;
      the refresh loop could never boot, since the initial poll would hit the
      not-yet-listening server).

### `subordinates.json`

The subordinates for this instance. Each row may define its own metadata,
metadata policy, constraints and additional claims; if any of it is omitted fall back to
the general settings in `config.yaml`.

```json
[
  {
    "entity_id": "https://sub.example.org",
    "description": "provisioned",
    "registered_entity_types": ["openid_relying_party"],
    "status": "active",
    "jwks": {
      "keys": [ { "kty": "EC", "crv": "P-256", "kid": "k1" } ]
    },
    "metadata_policy": { "openid_relying_party": { "id_token_signed_response_alg": { "value": "ES256" } } },
    "additional_claims": [ { "claim": "x", "value": "y", "crit": false } ]
  }
]
```

Row fields:

- `entity_id` (required), `description`, `registered_entity_types`,
  `metadata`, `metadata_policy`, `constraints` — see the
  [general-vs-per-subordinate precedence table](config.md#federation_data).
- `additional_claims` — per-subordinate claims that will be included in the
  subordinate statement; `crit: true` marks them as critical extensions.
- `enable_jwks_update`, `jwks_poll_interval` — automatic JWKS refresh for the
  subordinate's published keys.
- `status` — one of `active` / `blocked` / `pending` / `inactive`.

`endpoints.fetch.statement_lifetime` (config, default 600000 s ≈ 1 week)
sets the subordinate statement `exp`.

!!! info "active subordinates need keys"
    A row with `"status": "active"` and no `jwks` keys is a boot error
    (`status cannot be active without keys`). Set the keys or use a non-active
    status.

### `trust_mark_specs.json`

The **trust mark specs**, as a bare array: for each `trust_mark_type`, what
issued marks look like and who is eligible (`eligibility_config`). Specs must
exist for marks to be issued.

```json
[
  {
    "trust_mark_type": "https://example.org/trustmark",
    "lifetime": 3600,
    "ref": "https://example.org/trustmark/ref",
    "logo_uri": "https://example.org/logo.png",
    "delegation_jwt": "eyJ…",
    "additional_claims": { "key": "value" },
    "description": "Example trust mark",
    "cache_ttl": 60,
    "eligibility_config": { "mode": "db_only", "check_cache_ttl": 30 }
  }
]
```

Fields: `trust_mark_type` (unique), `lifetime` (issued-mark lifetime in
seconds), `ref`, `logo_uri`, `delegation_jwt`, `additional_claims`,
`description`, `cache_ttl` (seconds; issued marks are cached to avoid
re-signing), `eligibility_config`.

`eligibility_config.mode`:

- `db_only` (default) — eligibility comes only from the subjects in
  `trust_mark_subjects.json`.
- `check_only` — eligibility comes only from a checker.
- `db_or_check` — either source may grant eligibility.
- `db_and_check` — both sources must grant it.
- `custom` — a custom checker; **requires** `checker`.

`check_cache_ttl` (seconds) — eligibility results are cached and expire after
`check_cache_ttl` or after 5 minutes of disuse (approximate periodic sweep).

!!! info "spec validation is a boot error"
    An unknown `eligibility_config.mode` (valid: `db_only`, `check_only`,
    `db_or_check`, `db_and_check`, `custom`), or `custom` mode without a
    `checker`, is rejected at load.

### `trust_mark_subjects.json`

The **trust mark subjects**: who may receive a mark of a given type. The
file is a mapping from `trust_mark_type` to its subject rows (the type is
not repeated in every row). A subject's `status` controls what the issuance
endpoints answer (active → mark issued, blocked → refused, pending →
requestable). Blocking or inactivating a subject refuses **new** issuance;
already-issued marks are not affected by the file — see
[`trust_mark_instances.json`](#trust_mark_instancesjson).

```json
{
  "https://example.org/trustmark": [
    { "entity_id": "https://sub.example.org", "status": "active" }
  ]
}
```

!!! note
    `trust_mark_specs.json` and `trust_mark_subjects.json` belong together:
    subjects are matched against their type's spec, and mutations keep both
    files in sync. A subject whose spec is missing is skipped on load.

### `trust_mark_instances.json`

Every **issued trust mark JWT**, recorded at runtime (not provisioned by
hand), as a bare array. `trust_mark_subject_id` is derived from
type + entity. Used to answer `trust_mark_status` queries and the trust-mark
listing — `trust_mark_status` answers against these rows.

```json
[
  {
    "jti": "ca1a1536-dda2-4431-bf09-2cad16f16151",
    "created_at": 1793975573,
    "updated_at": 1793975573,
    "expires_at": 1793979173,
    "revoked": false,
    "trust_mark_subject_id": 8098244717992667663,
    "trust_mark_type": "https://example.org/trustmark",
    "subject": "https://sub.example.org"
  }
]
```

`trust_mark_subject_id` is a **content-addressed numeric ID**, derived with
FNV-1a-64 (low 63 bits) from `trust_mark_type` + the subject's `entity_id` —
the same derivation both `trust_mark_specs.json` and
`trust_mark_subjects.json` use for their rows, so an issued instance links to
the subject row across restarts. You never write or read it.

It is `0` when the subject has **no row** in `trust_mark_subjects.json`.
That only happens under the checker-based eligibility modes
(`check_only`, `db_or_check`, `custom`), where a checker — not a subject row
— grants eligibility. Under `db_only` (the default) and `db_and_check` the
subject row is the eligibility source, so a mark is issued only to an
entity that has an active subject row and the ID is always set.

!!! info "Blocking a subject does not revoke outstanding instances"
    A status change in `trust_mark_subjects.json` only refuses **new**
    issuance; it is picked up at the next boot and never rewrites this file.
    The `revoked` flag on an instance row is honored by `trust_mark_status`
    (and the listing excludes revoked rows), so revoke an issued mark by
    setting `"revoked": true` on its row here and restarting — no endpoint
    writes it (`ChangeSubjectStatus`, which revokes on subject block, is not
    wired in lighthouse-light). `trust_mark_status` answers `active` for
    signature-valid marks whose JTI is not tracked — see
    [endpoints.md](endpoints.md#trust-mark-status-semantics).

### `published_trust_marks.json`

The **trust marks this entity itself holds and advertises** in its entity
configuration `trust_marks` claim — including any self-issuance rule
(`self_issuance_spec`), as a bare array.

```json
[
  {
    "trust_mark_type": "https://example.org/trustmark",
    "trust_mark_issuer": "https://tmi.example.org",
    "trust_mark": "eyJ…",
    "refresh": false,
    "min_lifetime": 3600,
    "refresh_grace_period": 300,
    "refresh_rate_limit": 60,
    "self_issuance_spec": {
      "lifetime": 3600,
      "ref": "https://example.org/trustmark",
      "logo_uri": "https://example.org/logo.png",
      "additional_claims": { "key": "value" },
      "include_extra_claims_in_info": true
    }
  }
]
```

Each row advertises one trust mark. The row either carries the mark itself
(`trust_mark`), or describes how to obtain it — from the issuer via refresh,
or by issuing it to itself (`self_issuance_spec`):

| Field | Type | Description |
| --- | --- | --- |
| `trust_mark_type` | string | The trust mark type. Required unless it can be extracted from `trust_mark`. |
| `trust_mark_issuer` | string | Entity ID of the mark's issuer, advertised alongside the mark. Required unless it can be extracted from `trust_mark`. |
| `trust_mark` | string | The trust mark JWT itself (optional). If given, `trust_mark_issuer` and `trust_mark_type` are extracted from it at load and it is advertised as-is unless `refresh` is enabled. |
| `refresh` | boolean | Keep the mark fresh: re-fetch it from the issuer's `federation_trust_mark_endpoint` (or re-issue it via `self_issuance_spec`) when it nears expiry. Without a stored `trust_mark`, refresh is **always on**. `false` (default) advertises the stored JWT unchanged. |
| `min_lifetime` | integer (s) | Minimum remaining lifetime the mark must have before it counts as stale and is refreshed. Default `10`. |
| `refresh_grace_period` | integer (s) | While the mark is still valid (above `min_lifetime`) but closer to expiry than this, a background refresh is triggered. Default `3600`. |
| `refresh_rate_limit` | integer (s) | Minimum interval between refresh attempts; also the base of an exponential backoff (×2 per consecutive failure, capped at 1 h) after refresh errors. Default `60`. |
| `self_issuance_spec` | object | Makes the entity issue the mark **to itself** (the mark's `sub` is this entity's own ID) instead of relying on an external issuer — see below. Implies `refresh: true`. |

`self_issuance_spec` fields:

| Field | Type | Description |
| --- | --- | --- |
| `lifetime` | integer (s) | Lifetime of the issued mark. Without it the mark has no expiration (and never auto-refreshes). |
| `ref` | string | Reference URI of the trust mark spec the issued mark follows. |
| `logo_uri` | string | Logo embedded in the issued mark. |
| `additional_claims` | object | Extra claims embedded in the mark's `extra` block. |
| `include_extra_claims_in_info` | boolean | Also include `additional_claims` in the entity configuration's `trust_marks` info entry for this mark. |

A row that fails verification (no `trust_mark`, no valid
`trust_mark_issuer`/`trust_mark_type`, no `self_issuance_spec`) is skipped
with a warning when the entity configuration is built — it is not a boot
error.

### `jti.json`

Client-assertion **replay protection** entries, written at runtime when an
authenticated endpoint is used. The file is a bare object of
`"<jti>": <expiresUnixSeconds>` pairs; expired entries are reaped
automatically.

```json
{ "some-jti": 1793872800 }
```

## Persistence semantics

- **Content is only in the files.** The YAML config has no content sections;
  nothing is written from config into these files. Removing a row from a file
  removes it on the next boot.
- **Runtime rows survive.** Enrolled subordinates, issued trust mark
  instances, replay entries, and requested/approved subjects created through
  the endpoints persist here and survive restarts. They are never overwritten
  by the config.
- **Config-owned settings are never written to disk.** General settings from
  `config.yaml` (lifetimes, algorithm, rotation, entity metadata, policies,
  constraints, general claims) are applied to the in-memory KV store at every
  start — there is no `kv.json`. Entity-configuration extra claims are seeded
  from `federation_data.extra_entity_configuration_claims` into an in-memory
  store — there is no `additional_claims.json`. Removing a key from the
  config removes its effect at the next start — see [config.md](config.md).

## Scriptable provisioning

Provision a trust anchor + subordinate without any YAML content sections:

**Before the first start**, write `trust_anchors.json` and
`subordinates.json` as shown above. Start with a minimal config (`entity_id`,
`data_dir`, `signing`, `endpoints.fetch.path: /fetch`). Then:

```sh
# the served subordinate statement uses the file's row
curl -fsS "http://localhost:7672/fetch?sub=https://sub.example.org" | cut -d. -f2 | base64 -d
```

**Editing while stopped**: stop the instance (`SIGTERM`), edit the JSON,
start again. Runtime-created rows keep your edits; content removal is
removal.

**Verification**

```sh
python3 -m json.tool data/subordinates.json data/trust_anchors.json
```
