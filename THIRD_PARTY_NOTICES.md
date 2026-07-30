# Third-party notices

RepoMender depends on third-party software distributed under its own licenses.
Package manifests and lockfiles are the authoritative dependency inventories.

- Agent Compose is a separately deployed control plane licensed under
  AGPL-3.0. RepoMender communicates with it through its published API.
- Vinext, Next.js, React, Cloudflare tooling, go-oidc, OAuth2, pgx,
  PostgreSQL, Caddy, Go, and Node.js retain their respective copyright and
  license terms.
- Dex is used only by the local OIDC acceptance profile and retains its Apache
  License 2.0 terms.

Release artifacts must include this notice, the RepoMender license, and the
license inventory produced from the pinned dependency lockfiles.
