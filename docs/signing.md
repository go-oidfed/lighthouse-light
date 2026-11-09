# Signing keys

LightHouse-light always manages signing keys on the **filesystem**: the
`kms` / `pk_backend` / `pkcs11` options of LightHouse do not exist here.

- **Private keys** live in `signing.filesystem.key_file` (a single key file)
  or `signing.filesystem.key_dir` (a multi-key directory; rotated keys land
  here). At least one of the two is required — see [config.md](config.md).
- **Public keys** are published in the entity configuration's JWKS and the
  historical-keys endpoint. Private keys **never** appear in the data-dir
  JSON files.
- The signing key's `kid` is stable across restarts (it is only generated
  once and persists in the key dir / key file).

## First boot

With `auto_generate_keys: true` (default) a key for the configured algorithm
is created if none exists. It persists and is **not** regenerated on later
boots — the entity-configuration `kid` stays the same. With
`auto_generate_keys: false` and no key present, startup fails.

## Algorithm and key length

- `signing.alg` — signing algorithm, default `ES512` (`ES256`, `ES384`,
  `ES512`, `RS256`, …). Changing it triggers a new key.
- `signing.rsa_key_len` — RSA key length, default `2048` (RSA algorithms
  only).

Both are config-owned settings: they are applied from the config file at
every start and are never written into the data files. Removing `alg` from
the config falls back to the default (`ES512`).

!!! note "Changing `alg` keeps the old key"
    Changing `signing.alg` adds a new key for the new algorithm, but the old
    key stays in `key_dir` and **remains published in the entity
    configuration's JWKS** (it is not rotated out of the active set). In
    particular:

    - Both keys appear in the EC JWKS after the change; new statements are
      signed with the new key.
    - Superiors that have this entity as a subordinate keep only the keys
      you provisioned for them — a signature by the new key no longer
      verifies against their stored copy, so chain resolution, `jwks_update`
      and `jwks_update_trigger` fail against them until they refresh.
    - To phase the old key out, use `key_rotation` instead, or retire it
      manually (remove the old `<kid>.pem` from `key_dir` and restart).
    - The refresh endpoints verify newly received keys **against the keys
      currently stored for the subordinate**: a full key swap with no
      overlap is rejected (`jwks_update` → signature verification failure,
      `jwks_update_trigger` → `invalid_client`). An operator who rotates a
      subordinate's keys must publish the transition keys in the
      subordinate's EC JWKS *before* signing with them, or manually
      re-provision the subordinate row in `subordinates.json`.

## Rotation

- `signing.key_rotation.*` controls rotation. Rotation is disabled unless
  `interval` is configured, and it requires `key_dir` (a single `key_file`
  cannot rotate).
- **Validation**: an enabled rotation interval smaller than
  `federation_data.configuration_lifetime` is a configuration error and
  aborts startup. Below that bound the rotation loop degenerates into
  generating keys in a tight loop (unbounded key-dir growth and an
  ever-growing published JWKS) — even without that, an interval below the
  lifetime of the JWTs signed with the federation key invalidates them
  before they expire. Keep `interval` ≥ the longest lifetime of ECs,
  statements, and trust marks.
- Authority hints with a `jwks_sync` directive (see
  [data-directory.md](data-directory.md#authority_hintsjson)) are **notified
  automatically after each rotation** so superiors fetch the new keys.
