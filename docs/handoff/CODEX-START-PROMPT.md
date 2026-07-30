# Prompt for Codex on the home computer

Use the following prompt after cloning the repository:

```text
You are continuing RepoMender from a different computer.

Repository:
https://github.com/EasonW3300/repoMender

First inspect the current branch, worktree, remote branches, README, and these
documents:
- docs/handoff/HOME-CONTINUATION.md
- docs/acceptance/M02.md
- docs/roadmap/M03-M09.md

Treat Git and acceptance records as authoritative; do not rely on prior chat
history. Do not start M3 until M2's real GitLab.com smoke, CI, merge, and
m2-scm-repositories-mvp tag are complete.

Work strictly one module at a time. For every module:
1. create its feature branch from the accepted main branch;
2. implement the smallest verifiable MVP test-first;
3. keep it behind a feature flag;
4. run frontend, Go, database, contract, E2E, real-smoke, and security gates;
5. write docs/acceptance/M0X.md;
6. push, open a PR, wait for CI, merge only after acceptance, and tag it;
7. only then begin the next module.

Never upload or print .env contents, tokens, passwords, webhook/OAuth/OIDC
secrets, GitHub App PEM keys, cookies, or model credentials. Never allow an
Agent to auto-merge or write to a protected/default branch. AC model keys stay
only in the Agent Compose control plane.

Begin by reporting:
- exact checked-out commit and branch;
- clean/dirty worktree status;
- which M2 gates are already evidenced;
- the remaining blockers;
- the commands you will use to verify the transferred environment.

Then finish M2 before proceeding sequentially through M3-M9.
```
