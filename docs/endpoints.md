# Federation endpoints

There are 13 endpoint types. Each is enabled by its config section
(`path` and/or `url`); an endpoint with both empty is skipped.

- `path` — the internal HTTP route (e.g. `/fetch`).
- `url` — the external URL. When empty, derived from the entity ID + path;
  the result is advertised in the entity configuration metadata (e.g.
  `federation_fetch_endpoint`).
- Most endpoints accept `auth_enabled` and `auth_trust_anchors`; the registry
  in [config.md](config.md) notes exceptions.

## Endpoint types

| Type                | Config section                  | What it serves                                                                                                                                                                                                                            | Requires (provisioning)                                                                                       |
|---------------------|---------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| Fetch               | `endpoints.fetch`               | `GET/POST /fetch?sub=...` — a signed entity statement for the subordinate (subject must be active; unknown → 404 JSON)                                                                                                                    | `subordinates.json`                                                                                           |
| Subordinate listing | `endpoints.list`                | listing of subordinate entity IDs                                                                                                                                                                                                         | `subordinates.json` + `trust_mark_subjects.json`                                                              |
| Resolve             | `endpoints.resolve`             | trust-chain resolve (`.../resolve?sub=...&trust_anchor=...`, optional `type`) up to a trust anchor                                                                                                                                        | `allowed_trust_anchors` (must exist in `trust_anchors.json`); optional proactive resolver                     |
| Trust mark status   | `endpoints.trust_mark_status`   | `POST` with a `trust_mark` JWT → signed status-response JWT (`active` when the signature verifies and the JTI is known and not revoked/expired; an unknown JTI with a valid signature falls back to `active` based on expiry — see below) | `trust_mark_instances.json`                                                                                   |
| Trust mark listing  | `endpoints.trust_mark_list`     | `GET ?trust_mark_type=...` → JSON array of entities holding a valid issued mark                                                                                                                                                           | `trust_mark_instances.json`                                                                                   |
| Trust mark issuance | `endpoints.trust_mark`          | issues a trust mark JWT for an eligible subject (`sub`, `trust_mark_type`); non-eligible → JSON `not_eligible`                                                                                                                            | `trust_mark_subjects.json` + `trust_mark_specs.json` (specs, incl. eligibility) + `trust_mark_instances.json` |
| Trust mark request  | `endpoints.trust_mark_request`  | `GET ?sub=&trust_mark_type=` — marks the subject as pending                                                                                                                                                                               | `trust_mark_subjects.json`                                                                                    |
| Historical keys     | `endpoints.historical_keys`     | signed JWK Set (`jwk-set+jwt`) with the **retired** keys — revoked and expired keys only; the currently valid key is *not* included and the set is empty until the first rotation                                                         | none (served from the key dir)                                                                                |
| Enroll              | `endpoints.enroll`              | enroll endpoint with an entity checker (`checker.type`/`config`; checker trust anchors must exist in `trust_anchors.json`)                                                                                                                | `subordinates.json` + optional checker                                                                        |
| Enroll request      | `endpoints.enroll_request`      | staged enrollment request                                                                                                                                                                                                                 | `subordinates.json`                                                                                           |
| Entity collection   | `endpoints.entity_collection`   | `GET` — collected entities; periodic + paginated modes; `disable_delegated_collection` suppresses advertised sub-collection endpoints                                                                                                     | `allowed_trust_anchors`; `interval` → periodic collection                                                     |
| JWKS update         | `endpoints.jwks_update`         | `POST` a signed JWK Set (`application/jwk-set+jwt`, `iss==sub`) from a subordinate replacing its stored keys (signature verified against the currently stored keys)                                                                       | `subordinates.json`                                                                                           |
| JWKS update trigger | `endpoints.jwks_update_trigger` | tells this entity to re-fetch a subordinate's keys from its entity configuration                                                                                                                                                          | `subordinates.json`                                                                                           |

The entity configuration advertises a `federation_*_endpoint` for every
registered endpoint, including the JWKS-update and trigger endpoints.

### Trust mark status semantics

`trust_mark_status` returns a signed status JWT with `status`: `active`,
`expired`, or `invalid`:

- The trust mark's signature must verify against this entity's federation
  keys and `iss` must be this entity — otherwise `invalid`.
- With a valid signature, the JTI is looked up in `trust_mark_instances.json`
  (`revoked` wins, then `expired`, then `active`).
- A validly-signed mark whose JTI is **not** tracked (unknown JTI, e.g. after
  the instances file was reset) falls back to expiry-only: `active` while
  unexpired. There is no 404 for signature-valid marks.
- The `revoked` flag on an instance row **is honored**: set
  `"revoked": true` on a row in `trust_mark_instances.json` (the file is
  loaded at boot, so restart afterwards), and `trust_mark_status` answers
  `revoked` for that JTI — `revoked` takes precedence over `active`/`expired`.
  The trust-mark listing and `HasActiveInstance` already exclude revoked
  rows.
- No runtime **endpoint** writes `revoked: true`: the store's
  `ChangeSubjectStatus` (which revokes a subject's instances when it is
  blocked/inactivated) is not wired to any endpoint, and blocking a subject
  in `trust_mark_subjects.json` only refuses new issuance. Revocation is an
  operator action on the file, as with all content here.

The trust mark **listing** answers exclusively against the instance rows
(non-revoked, non-expired subjects), so an entity appears there only after a
mark was actually issued to it.

## Endpoint authentication

Auth-enabled endpoints use the OIDC Federation `private_key_jwt` client
authentication scheme:

- Enable per endpoint with `auth_enabled: true`, or globally with
  `endpoints.auth.all_require_auth: true`.
- `auth_trust_anchors` per endpoint; when empty, an auth-enabled endpoint
  falls back to `endpoints.auth.trust_anchors` (entity IDs). A reference to
  an anchor that is not provisioned in `trust_anchors.json` fails at boot,
  and an auth-enabled endpoint with no anchors at all is a validation error.
- Auth-enabled endpoints are registered as **POST**; unauthenticated ones as
  GET.

When a client calls an authenticated endpoint, the server checks, in order:

1. the `client_assertion` JWT is present and well-formed (`iss==sub` = the
   client entity, `aud` = this entity ID, valid `iat`/`exp`);
2. the assertion's `jti` is not a replay — fresh values are recorded in
   `jti.json` (expired entries are reaped);
3. the client's trust chain resolves to one of the configured trust anchors
   (anchor keys from `trust_anchors.json`);
4. the assertion signature verifies against the client's entity-configuration
   keys.

Failures return OAuth 2.0 JSON errors (`invalid_request` / `invalid_client` /
`invalid_grant`), never 500s. For auth to succeed, the client must be able to
reach you (its `aud`) and you must be able to reach the client's entity
configuration to build the chain.

## Path / URL rules

- Only `path` set: the route is internal; the published URL is
  `<entity_id><path>`.
- `url` set without `path`: nothing is registered internally (the URL is only
  used in published metadata).
- Routes are registered internally; `/.well-known/openid-federation` takes
  precedence over the endpoint routes.

## Verification

```sh
# fetch a subordinate statement
curl -fsS "http://localhost:7672/fetch?sub=https://sub.example.org"

# trust mark status: POST the issued JWT
curl -fsS -X POST -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "trust_mark=$TM" http://localhost:7672/trust_mark_status

# list entities with valid marks
curl -fsS "http://localhost:7672/trust_mark_list?trust_mark_type=https://example.org/trustmark"

# subordinate rotates its keys (signed JWK Set, iss==sub==entity)
curl -fsS -X POST -H "Content-Type: application/jwk-set+jwt" \
  --data-binary @signed.jwk-set.jwt http://localhost:7672/jwks_update
```