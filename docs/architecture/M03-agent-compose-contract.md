# M03 Agent Compose contract

RepoMender inspected the Agent Compose fork at commit
`e736b04d` before defining its execution boundary. Agent Compose remains an
independently deployed control plane; no AC sandbox, Agent, or model credential
is copied into RepoMender.

## Transport and authentication

- Daemon HTTP base URL is configured with `REPOMENDER_AC_BASE_URL`.
- TCP/HTTP deployments may require
  `Authorization: Bearer <REPOMENDER_AC_AUTH_TOKEN>`.
- RepoMender never persists or streams the token.
- Health/build metadata uses `GET /api/version`.
- Execution uses ConnectRPC service generation
  `agentcompose.v2`, principally `RunService`.

The relevant Connect procedures are:

```text
/agentcompose.v2.RunService/StartRun
/agentcompose.v2.RunService/RunAgentStream
/agentcompose.v2.RunService/GetRun
/agentcompose.v2.RunService/FollowRunLogs
/agentcompose.v2.RunService/StopRun
```

## Health response

`GET /api/version` retains the legacy envelope:

```json
{
  "err": null,
  "msg": "OK",
  "data": {
    "version": "0",
    "timestamp": 1785466633.1333659,
    "timezone": "CST",
    "timezone_offset": 28800,
    "os": "linux",
    "arch": "arm64",
    "compiled_drivers": ["docker"]
  }
}
```

The daemon's `version` field is a build version, not the Connect API
generation. RepoMender therefore:

1. identifies the execution API by the `agentcompose.v2` procedures;
2. requires a configured runtime driver, `docker` by default;
3. applies an exact build-version constraint only when
   `REPOMENDER_AC_REQUIRED_VERSION` is non-empty.

This supports released version pins while allowing AC development images that
report version `0`.

## RepoMender adapter boundary

Business modules depend only on `internal/execution.Adapter`. The boundary
defines health, start, ordered events, cancel, and final-result operations.
Requests carry an immutable commit SHA, repository identity, prompt, deadline,
and resource policy. AC-specific messages are normalized to RepoMender event
kinds before they can reach persistence or SSE.

`StartRun` receives the immutable repository and commit in both the guarded
prompt context and a versioned `payloadJson`. `FollowRunLogs` uses Connect's
five-byte streaming envelope and exposes the AC byte offset as the reconnect
sequence. A RepoMender deadline closes the stream and issues `StopRun`.
`GetRun` results must contain a JSON `schemaVersion`; failed and cancelled runs
are mapped before schema validation.

The stable failure codes are:

```text
ac_unavailable
ac_rejected
ac_incompatible
ac_timeout
ac_cancelled
ac_sandbox_failed
ac_agent_failed
ac_malformed_response
ac_stream_dropped
```

## Readiness behavior

When `REPOMENDER_FEATURE_M3_AC_EXECUTION=false`, AC has no effect on runtime
behavior. When enabled, database and AC checks must both pass
`/health/ready`. `/health/live` remains independent of both external
dependencies so an orchestrator does not restart a healthy API process during
an AC outage.

## Verified local daemon

On 2026-07-31, the local daemon at `127.0.0.1:7410` returned:

```text
version=0
os=linux
arch=arm64
compiled_drivers=docker
```

This confirms the health envelope and local runtime capability. A real Codex
run `46973800f0d9…` successfully exercised `StartRun`, ordered log streaming,
and a terminal event. The run then failed because the AC sandbox could not
reach `https://api.openai.com/v1/responses` without the host's local proxy.
RepoMender maps that terminal outcome to `ac_agent_failed`.

An explicitly authorized temporary proxy bridge proved outbound transport and
changed the real failure to OpenAI HTTP 401, confirming the AC control plane
still needs an OpenAI-family credential. Real run `b97d4d9eecbd…` then proved
`StopRun`, persisted cancellation, a terminal SSE event, and
`ac_cancelled` result mapping. The bridge and temporary Agent proxy variables
were removed afterward.

The M3 gate remains open until a real run succeeds. Unit and HTTP fixtures
cover cancellation, deadline-driven
`StopRun`, reconnect offsets, malformed frames, dropped streams, redaction,
RBAC, CSRF, and stable SSE errors.
