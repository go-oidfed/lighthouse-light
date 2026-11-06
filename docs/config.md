# Configuration reference

The config file is located by the first match of, in order:

1. the first CLI argument (e.g. `lighthouse-light /etc/lighthouse/config.yaml`);
2. the `$LH_CONFIG_FILE` environment variable;
3. `config.yaml` in the first existing of the discovery locations:
    - `.`
    - `config`
    - `/config`
    - `/lighthouse/config`
    - `/lighthouse`
    - `/data/config`,
    - `/data`
    - `/etc/lighthouse`

If none matches, startup fails with a resolution error naming the searched
locations.

Relative paths in the config resolve against the config file's directory
(`signing.filesystem.key_file/key_dir`, `federation_data.metadata_policy_file`,
`server.tls.cert/key`).

A runnable commented example ships as `config-example.yaml`.

---

## `entity_id`

<span class="badge badge-purple" title="Value Type">URL</span>
<span class="badge badge-red" title="If this option is required or optional">required</span>

The OpenID Federation Entity Identifier for this instance.
This is a URI that uniquely identifies your entity within the federation and is used as the `iss` and `sub` claims in the Entity Configuration JWT.

The entity ID is typically the base URL where your federation endpoints are served.

??? file "config.yaml"

    ```yaml
    entity_id: https://ta.example.org
    ```

## `data_dir`

<span class="badge badge-purple" title="Value Type">directory path</span>
<span class="badge badge-blue" title="Default Value">/data</span>

**All federation content and runtime state** lives here as plain JSON files
(9 files, see [data-directory.md](data-directory.md)). One instance per data
dir.

??? file "config.yaml"

    ```yaml
    data_dir: /data
    ```

## `logging`

Under the `logging` config option the logging behavior is configured.
LightHouse-light writes its logs to stdout (no log files, no banner options).

### `logging.level`
<span class="badge badge-purple" title="Value Type">enum</span>
<span class="badge badge-blue" title="Default Value">info</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The `level` option sets the minimal log level that should be logged.

Valid values are:

- `trace`
- `debug`
- `info`
- `warn`
- `error`
- `fatal`
- `panic`

### `logging.format`
<span class="badge badge-purple" title="Value Type">enum</span>
<span class="badge badge-blue" title="Default Value">json</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The `format` option selects the output format for the logs written to stdout.

Valid values are:

- `json` — structured JSON output for machine parsing (default)
- `console` — human-friendly console output

??? file "config.yaml"

    ```yaml
    logging:
      level: info
      format: console
    ```

## `server`

Under the `server` config option the (HTTP) server is configured. Native TLS
is supported; without it (default) terminate TLS at a reverse proxy
(Caddy/Traefik/nginx) — see [deployment.md](deployment.md).

### `server.ip_listen`
<span class="badge badge-purple" title="Value Type">string</span>
<span class="badge badge-blue" title="Default Value">0.0.0.0</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The `ip_listen` config option sets the network address to which the server
binds. If omitted, `0.0.0.0` is used. Usually `127.0.0.1` when a reverse proxy
is in front; `0.0.0.0` in containers.

??? file "config.yaml"

    ```yaml
    server:
      ip_listen: 127.0.0.1
    ```

### `server.port`
<span class="badge badge-purple" title="Value Type">integer</span>
<span class="badge badge-blue" title="Default Value">7672</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The `port` config option sets the port at which the server starts and listens
for incoming requests. It is only used if `tls` is not enabled; if `tls` is
enabled, port `443` is used (and optionally port `80`).

??? file "config.yaml"

    ```yaml
    server:
      port: 4242
    ```

### `server.tls`
<span class="badge badge-purple" title="Value Type">object / mapping</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Configuration for native HTTPS. If `tls` is enabled, port `443` is used (and,
with `redirect_http`, port `80` redirects to HTTPS). Managing certificates
yourself (certbot / a CA) is fine; so is leaving TLS to a reverse proxy.

??? file "config.yaml"

    ```yaml
    server:
      tls:
        enabled: true
        redirect_http: true
        cert: /data/tls/cert.pem
        key: /data/tls/key.pem
    ```

#### `server.tls.enabled`
<span class="badge badge-purple" title="Value Type">boolean</span>
<span class="badge badge-blue" title="Default Value">false</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Enable HTTPS instead of plain HTTP. `cert` and `key` must be set when enabled
(validation error `server.tls: cert and key are required when enabled`
otherwise).

#### `server.tls.redirect_http`
<span class="badge badge-purple" title="Value Type">boolean</span>
<span class="badge badge-blue" title="Default Value">false</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The `redirect_http` option determines if port `80` should be redirected to
port `443`.

#### `server.tls.cert`
<span class="badge badge-purple" title="Value Type">file path</span>
<span class="badge badge-red" title="If this option is required or optional">required for TLS</span>

Path to the TLS certificate (PEM). Relative paths resolve against the config
file's directory.

#### `server.tls.key`
<span class="badge badge-purple" title="Value Type">file path</span>
<span class="badge badge-red" title="If this option is required or optional">required for TLS</span>

Path to the TLS private key (PEM). Relative paths resolve against the config
file's directory.

### `server.trusted_proxies`
<span class="badge badge-purple" title="Value Type">list of strings</span>
<span class="badge badge-blue" title="Default Value">[]</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The `trusted_proxies` option configures a list of trusted proxies by IP
address or network range (CIDR notation).

If the server runs behind a proxy or load balancer, certain header information
may be sent using special `X-Forwarded-*` headers (e.g. the client's real IP
address). If set, such header information is only honored when the request
comes via one of the trusted proxies. If unset, the information is always read
from the headers, which might be spoofed.

??? file "config.yaml"

    ```yaml
    server:
      trusted_proxies:
        - "10.0.0.0/8"
        - "172.16.0.0/12"
        - "192.168.0.0/16"
        - "fc00::/7"
    ```

### `server.forwarded_ip_header`
<span class="badge badge-purple" title="Value Type">string</span>
<span class="badge badge-blue" title="Default Value">X-Forwarded-For</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The `forwarded_ip_header` option specifies which HTTP header to use for
getting the client's real IP address when behind a proxy.

??? file "config.yaml"

    ```yaml
    server:
      forwarded_ip_header: X-Real-IP
    ```

### `server.cors`
<span class="badge badge-purple" title="Value Type">object / mapping</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Configuration for CORS (Cross-Origin Resource Sharing) on the server. CORS
allows web browsers to make requests to the federation endpoints from
different origins, useful when building web applications that consume them.

??? file "config.yaml"

    ```yaml
    server:
      cors:
        enabled: true
        allow_origins: "*"
        allow_methods: "GET,POST,HEAD,PUT,DELETE,PATCH"
        allow_headers: "Origin,Content-Type,Accept"
        allow_credentials: false
        max_age: 3600
    ```

#### `server.cors.enabled`
<span class="badge badge-purple" title="Value Type">boolean</span>
<span class="badge badge-blue" title="Default Value">false</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Enables or disables the CORS middleware. When disabled, no CORS headers are
sent and cross-origin requests from browsers are blocked.

#### `server.cors.allow_origins`
<span class="badge badge-purple" title="Value Type">string</span>
<span class="badge badge-blue" title="Default Value">*</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Comma-separated list of allowed origins, or `*` to allow all origins.

??? file "config.yaml"

    ```yaml
    server:
      cors:
        allow_origins: "https://example.com,https://app.example.com"
    ```

#### `server.cors.allow_methods`
<span class="badge badge-purple" title="Value Type">string</span>
<span class="badge badge-blue" title="Default Value">GET,POST,HEAD,PUT,DELETE,PATCH</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Comma-separated list of allowed HTTP methods.

#### `server.cors.allow_headers`
<span class="badge badge-purple" title="Value Type">string</span>
<span class="badge badge-blue" title="Default Value">empty (uses Fiber defaults)</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Comma-separated list of allowed request headers.

#### `server.cors.allow_credentials`
<span class="badge badge-purple" title="Value Type">boolean</span>
<span class="badge badge-blue" title="Default Value">false</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Indicates whether the request can include user credentials like cookies or
HTTP authentication.

!!! warning
    When `allow_credentials` is `true`, `allow_origins` cannot be `*`; you
    must specify explicit origins.

#### `server.cors.expose_headers`
<span class="badge badge-purple" title="Value Type">string</span>
<span class="badge badge-blue" title="Default Value">empty</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Comma-separated list of headers that browsers are allowed to access.

#### `server.cors.max_age`
<span class="badge badge-purple" title="Value Type">integer</span>
<span class="badge badge-blue" title="Default Value">0</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

How long (in seconds) browsers should cache preflight request results. `0`
means no caching.

## `signing`

Under the `signing` config option the key management and signatures are
configured. There are **no `kms` / `pk_backend` / `pkcs11`/HSM options**:
key management is fixed to the filesystem (see [signing.md](signing.md)).

### `signing.auto_generate_keys`
<span class="badge badge-purple" title="Value Type">boolean</span>
<span class="badge badge-blue" title="Default Value">true</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

When set to `true`, a signing key for the configured algorithm is generated at
startup if none exists. When set to `false`, startup fails if the required
private key is not present.

### `signing.filesystem`
<span class="badge badge-purple" title="Value Type">object / mapping</span>
<span class="badge badge-red" title="If this option is required or optional">required (key_file or key_dir)</span>

Configuration for the filesystem private-key storage. Relative paths resolve
against the config dir.

??? file "config.yaml"

    ```yaml
    signing:
      filesystem:
        key_dir: /data/keys
    ```

#### `signing.filesystem.key_dir`
<span class="badge badge-purple" title="Value Type">directory path</span>
<span class="badge badge-red" title="If this option is required or optional">required, unless `key_file` is given</span>

The `key_dir` option specifies the path to a directory that contains the
private signing key(s). Keys are stored using the naming convention `<kid>.pem`.

#### `signing.filesystem.key_file`
<span class="badge badge-purple" title="Value Type">file path</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The `key_file` option can be used as an alternative to `key_dir` if only a
single signing key is used and no key rotation happens. Use `key_dir` instead
when rotation is needed.

At least one of `key_file` / `key_dir` is required (validation error
`signing.filesystem: key_file or key_dir must be set` otherwise).

### `signing.alg`
<span class="badge badge-purple" title="Value Type">enum</span>
<span class="badge badge-blue" title="Default Value">ES512</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The signing algorithm to use for Entity Configurations, Entity Statements, and
Trust Marks. Changing it takes effect at the next start.

Supported values include:

- **ECDSA** — `ES256`, `ES384`, `ES512`
- **secp256k1** — `ES256K`
- **EdDSA** — `Ed25519`, `Ed448`
- **RSA** — `RS256`, `RS384`, `RS512`, `PS256`, `PS384`, `PS512`
- **ML-DSA (post-quantum)** — `ML-DSA-44`, `ML-DSA-65`, `ML-DSA-87`
- **Composite (PQC-hybrid)** — `ML-DSA-44-ES256`, `ML-DSA-65-ES256`,
  `ML-DSA-87-ES384` (ML-DSA + ECDSA), `ML-DSA-44-Ed25519`,
  `ML-DSA-65-Ed25519`, `ML-DSA-87-Ed448` (ML-DSA + EdDSA)

### `signing.rsa_key_len`
<span class="badge badge-purple" title="Value Type">integer</span>
<span class="badge badge-blue" title="Default Value">2048</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The RSA key length (in bits) used when generating RSA-based signing keys. Only
relevant when the signing algorithm is RSA-based.

### `signing.key_rotation`
<span class="badge badge-purple" title="Value Type">object / mapping</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Configuration for automatic signing key rotation. When enabled, new signing
keys are generated according to the configured interval and both current and
next public keys are published. Requires `key_dir` (a single `key_file` cannot
rotate).

??? file "config.yaml"

    ```yaml
    signing:
      key_rotation:
        enabled: true
        interval: 30d
        overlap: 1h
    ```

#### `signing.key_rotation.enabled`
<span class="badge badge-purple" title="Value Type">boolean</span>
<span class="badge badge-blue" title="Default Value">false</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Enables automatic key rotation.

#### `signing.key_rotation.interval`
<span class="badge badge-purple" title="Value Type">duration</span>
<span class="badge badge-blue" title="Default Value">~1 week</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The interval at which keys are rotated. This defines the lifetime of each key.

!!! warning
    The interval should not be smaller than the lifetime of Entity
    Configurations, Entity Statements, Trust Marks, or other JWTs signed with
    the federation key.

#### `signing.key_rotation.overlap`
<span class="badge badge-purple" title="Value Type">duration</span>
<span class="badge badge-blue" title="Default Value">1 hour</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The overlap period between the current and next key. During this window both
keys are valid, and the server transitions to using the new key somewhere in
the middle of the period.

#### `signing.key_rotation.key_announcement_lead_time`
<span class="badge badge-purple" title="Value Type">duration</span>
<span class="badge badge-blue" title="Default Value">max(5 × entity-configuration lifetime, 24h)</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The **key announcement lead time** controls how far in advance a new key is
published in the JWKS before it becomes the active signing key, giving clients
time to fetch and cache the new key before the old key is retired.

If unset, the lead time is resolved as:

1. `key_announcement_lead_time_ec_multiplier` × entity-configuration lifetime,
   if the multiplier is `> 0`;
2. otherwise the fixed `key_announcement_lead_time`, if `> 0`;
3. otherwise `max(5 × entity-configuration lifetime, 24h)`.

The result is clamped to at least one entity-configuration lifetime.

#### `signing.key_rotation.key_announcement_lead_time_ec_multiplier`
<span class="badge badge-purple" title="Value Type">float</span>
<span class="badge badge-blue" title="Default Value">0</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Multiplier for the entity-configuration lifetime used to compute the
announcement lead time. Takes precedence over
`key_announcement_lead_time` when `> 0`.

## `federation_data`

General (entity-level) federation settings. These settings apply to the entity
as a whole; content collections (subordinates, trust anchors, authority hints,
trust marks, issuers/owners) live in the data files, not in this config — see
[data-directory.md](data-directory.md).

Metadata-like content exists at **two levels** — a general (entity-level)
value in the config and a per-subordinate override in `subordinates.json`:

| Content            | General (entity) level — applies to ALL subordinates | Per-subordinate — wins over general |
|--------------------|-------------------------------------------------------|--------------------------------------|
| Metadata           | `federation_data.metadata` (config)                   | `subordinates.json[] .metadata`      |
| Metadata policies  | `federation_data.metadata_policy_file` (config)       | `subordinates.json[] .metadata_policy` |
| Constraints        | `federation_data.constraints` (config)                | `subordinates.json[] .constraints`   |
| Additional claims  | `federation_data.subordinate_additional_claims` (config) | `subordinates.json[] .additional_claims` |
| Entity-config extra claims | `federation_data.extra_entity_configuration_claims` (config) | — (entity configuration, not per subordinate) |

A subordinate that defines its own value for a field uses it; otherwise the
general value applies. The general fallback is resolved at read time and is
**never written into `subordinates.json`**. Per-subordinate claims become
`extra` claims in the signed statement; `crit: true` marks them as critical
extensions.

### `federation_data.metadata`
<span class="badge badge-purple" title="Value Type">object / mapping</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

General (entity-level) metadata applied to the statements of **all**
subordinates, unless a subordinate defines its own (`subordinates.json`
`metadata`). The object maps entity types to their claims, per the OpenID
Federation Specification.

??? file "config.yaml"

    ```yaml
    federation_data:
      metadata:
        openid_provider:
          organization_name: "Example Organization"
    ```

### `federation_data.constraints`
<span class="badge badge-purple" title="Value Type">object / mapping</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Constraints object included in subordinate statements, as defined in the OpenID
Federation Specification. Applied to the statements of **all** subordinates
unless a subordinate defines its own (`subordinates.json` `constraints`).

??? file "config.yaml"

    ```yaml
    federation_data:
      constraints:
        max_path_len: 2
        naming_constraints:
          permitted: [".example.com"]
          excluded: ["east.example.com"]
        allowed_entity_types: ["openid_provider", "openid_relying_party"]
    ```

### `federation_data.metadata_policy_crit`
<span class="badge badge-purple" title="Value Type">list of strings</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Critical metadata policy operators as per the OpenID Federation Specification
(e.g. `["one_of"]`). Only operators actually used in a statement's policy are
marked critical.

### `federation_data.metadata_policy_file`
<span class="badge badge-purple" title="Value Type">file path</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Path (relative → config dir) to a JSON file holding the **general metadata
policies applicable to all subordinates**, as defined in the OpenID Federation
Specification (the document format `{"<entity_type>": {"<operator>": {...}}}`).
Overridden per subordinate by `subordinates.json[] .metadata_policy`. A missing
file is skipped with a warning; a malformed file fails startup.

### `federation_data.configuration_lifetime`
<span class="badge badge-purple" title="Value Type">duration</span>
<span class="badge badge-blue" title="Default Value">24h</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

The lifetime of the published Entity Configuration JWT. This defines how long
it is valid before it must be re-fetched (its `exp`).

### `federation_data.federation_entity_metadata`
<span class="badge badge-purple" title="Value Type">object / mapping</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Entity metadata included in the `metadata` claim of the Entity Configuration
for the `federation_entity` entity type.

Federation endpoint URLs (fetch, list, resolve, …) are added to this metadata
automatically from `endpoints.*`; you do not need to list them here.

Common fields:

| Field               | Type            | Description                |
|---------------------|-----------------|----------------------------|
| `display_name`      | string          | Display name of the entity |
| `description`       | string          | Description of the entity  |
| `keywords`          | list of strings | Keywords for the entity    |
| `contacts`          | list of strings | Contact email addresses    |
| `logo_uri`          | uri             | Logo URI                   |
| `policy_uri`        | uri             | Policy URI                 |
| `information_uri`   | uri             | Information URI            |
| `organization_name` | string          | Organization name          |
| `organization_uri`  | uri             | Organization URI           |
| anything else       | json            | Additional metadata fields |

??? file "config.yaml"

    ```yaml
    federation_data:
      federation_entity_metadata:
        display_name: "TA Example"
        organization_name: "Example Organization"
        contacts: ["admin@example.org"]
    ```

### `federation_data.subordinate_additional_claims`
<span class="badge badge-purple" title="Value Type">list of objects</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

General fallback additional claims included in the statement of every
subordinate that has no claims of its own. Each claim can be individually
marked as critical. A subordinate with its own claims
(`subordinates.json[] .additional_claims`) keeps them.

Each claim object has the following fields:

| Field   | Type    | Description                                  |
|---------|---------|----------------------------------------------|
| `claim` | string  | Claim name                                   |
| `value` | any     | Claim value (any JSON type)                  |
| `crit`  | boolean | Whether the claim is marked as critical      |

??? file "config.yaml"

    ```yaml
    federation_data:
      subordinate_additional_claims:
        - claim: custom_scope
          value: "read write"
          crit: false
    ```

### `federation_data.extra_entity_configuration_claims`
<span class="badge badge-purple" title="Value Type">list of objects</span>
<span class="badge badge-blue" title="Default Value">[]</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Additional claims included in the **entity configuration** — they are added
as extra claims of the signed entity-configuration JWT payload (top-level
members alongside `metadata`, `authority_hints`, …). Each claim can be
individually marked as critical. The `lighthouse_version` claim is appended
automatically. Seeded into the in-memory additional claims store at boot —
there is no `additional_claims.json` file.

Each claim object has the same fields as
[`subordinate_additional_claims`](#federation_datasubordinate_additional_claims):

| Field   | Type    | Description                                  |
|---------|---------|----------------------------------------------|
| `claim` | string  | Claim name                                   |
| `value` | any     | Claim value (any JSON type)                  |
| `crit`  | boolean | Whether the claim is marked as critical      |

??? file "config.yaml"

    ```yaml
    federation_data:
      extra_entity_configuration_claims:
        - claim: x
          value: "y"
          crit: false
    ```

## `endpoints`

Registers the 13 federation endpoint types (see [endpoints.md](endpoints.md);
the types map to the OpenID Federation specification as `fetch` §8.1, `list`
§8.2, `resolve` §8.3, `trust_mark_status` §8.4, `trust_mark_listing` §8.5,
`trust_mark` §8.6, `historical_keys` §8.7).

Each enabled endpoint takes:

| Field                | Description                                                                                              |
|----------------------|----------------------------------------------------------------------------------------------------------|
| `path`               | URL path under which the endpoint is served. Set to `null`/empty to disable the endpoint.                 |
| `url`                | External URL published in the Entity Configuration. If not set, derived from `<entity_id><path>`.         |
| `auth_enabled`       | When `true`, requires `private_key_jwt` client authentication.                                           |
| `auth_trust_anchors` | Trust anchor entity IDs used to verify client assertions (when `auth_enabled` is `true`).                |

An endpoint is registered when `path` or `url` is set; an endpoint with both
empty is skipped.

### `endpoints.auth.all_require_auth`
<span class="badge badge-purple" title="Value Type">boolean</span>
<span class="badge badge-blue" title="Default Value">false</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Forces `auth_enabled` on every endpoint.

### `endpoints.auth.trust_anchors`
<span class="badge badge-purple" title="Value Type">list of entity IDs</span>
<span class="badge badge-blue" title="Default Value">[]</span>
<span class="badge badge-green" title="If this option is required or optional">optional</span>

Global fallback `auth_trust_anchors` for auth-enabled endpoints. **Entity IDs
only** — the anchors' definitions (JWKS) must be provisioned in
`trust_anchors.json`; a reference to a missing anchor is a boot error.

### Endpoint sections

Each of the 13 sections takes the common fields above plus:

<table>
<thead><tr><th>Section</th><th>Extra keys</th></tr></thead>
<tbody>
<tr><td class="highlight"><code>endpoints.fetch</code></td><td><code>statement_lifetime</code> (duration — the lifetime of Entity Statements issued by the Fetch endpoint; default <code>600000s</code> ≈ 1 week)</td></tr>
<tr><td class="highlight"><code>endpoints.list</code></td><td>—</td></tr>
<tr><td class="highlight"><code>endpoints.resolve</code></td><td><code>allowed_trust_anchors</code> (must exist in <code>trust_anchors.json</code>), <code>use_entity_collection_allowed_trust_anchors</code> (use the collection's anchor list instead), <code>grace_period</code>, <code>time_elapsed_grace_factor</code>, <code>proactive_resolver.{enabled, concurrency_limit, queue_size, response_storage.{dir, store_json, store_jwt}}</code></td></tr>
<tr><td class="highlight"><code>endpoints.trust_mark_status</code></td><td>—</td></tr>
<tr><td class="highlight"><code>endpoints.trust_mark_list</code></td><td>—</td></tr>
<tr><td class="highlight"><code>endpoints.trust_mark</code></td><td>— (trust mark <strong>specs</strong> are provisioned in <code>trust_mark_specs.json</code>, not here)</td></tr>
<tr><td class="highlight"><code>endpoints.historical_keys</code></td><td>—</td></tr>
<tr><td class="highlight"><code>endpoints.enroll</code></td><td><code>checker</code> (<code>{type, config}</code>; checker trust anchors must exist in <code>trust_anchors.json</code>)</td></tr>
<tr><td class="highlight"><code>endpoints.enroll_request</code></td><td>—</td></tr>
<tr><td class="highlight"><code>endpoints.trust_mark_request</code></td><td>—</td></tr>
<tr><td class="highlight"><code>endpoints.entity_collection</code></td><td><code>allowed_trust_anchors</code>, <code>interval</code>, <code>concurrency_limit</code>, <code>pagination_limit</code>, <code>disable_delegated_collection</code> (suppresses the advertised sub-collection endpoints)</td></tr>
<tr><td class="highlight"><code>endpoints.jwks_update</code></td><td>—</td></tr>
<tr><td class="highlight"><code>endpoints.jwks_update_trigger</code></td><td>—</td></tr>
</tbody>
</table>

??? file "config.yaml"

    ```yaml
    endpoints:
      auth:
        all_require_auth: false
        trust_anchors: ["https://root.example.org"]
      fetch:
        path: /fetch
        auth_enabled: true
      jwks_update:
        path: /jwks_update
      jwks_update_trigger:
        path: /jwks_update_trigger
    ```

## Configuration errors (fail fast, never silent)

Misconfiguration is reported at startup with the offending field:

- `entity_id is required`
- `signing.filesystem: key_file or key_dir must be set`
- `server.tls: cert and key are required when enabled`
- `server.admin_tls is not supported (there is no admin API)`
- `endpoint X: auth_enabled requires auth_trust_anchors or endpoints.auth.trust_anchors`
- `endpoints.auth.trust_anchors: trust anchor "..." is not defined in trust_anchors.json`
  (and the equivalent per-endpoint checks)

Data-file load errors (also fatal, filename included) are listed in
[data-directory.md](data-directory.md).