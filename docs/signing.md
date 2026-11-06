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

## Rotation

- `signing.key_rotation.*` controls rotation. Rotation is disabled unless
  `interval` is configured, and it requires `key_dir` (a single `key_file`
  cannot rotate).
- Authority hints with a `jwks_sync` directive (see
  [data-directory.md](data-directory.md#authority_hintsjson)) are **notified
  automatically after each rotation** so superiors fetch the new keys.
