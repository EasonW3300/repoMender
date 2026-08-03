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

On 2026-08-03, the local daemon at `127.0.0.1:7410` returned:

```text
version=0
os=linux
arch=arm64
compiled_drivers=docker
```

This confirms the health envelope and local runtime capability. Earlier real
runs `46973800f0d9…` and `b97d4d9eecbd…` exercised ordered streaming,
terminal failure mapping, and cancellation. Those runs also documented the
failure mode when the AC sandbox cannot reach an external model gateway
without an approved egress path.

After the AC provider was configured for the official DeepSeek Responses
endpoint and the user authorized the smoke payload, run
`704de22a8d3977987ab3e22ddd46ed9813990d2bf67731052960613c33d15cb7` completed
successfully. RepoMender received ordered log and terminal events, AC
persisted `RUN_STATUS_SUCCEEDED`, and the adapter returned a schema-valid
`v1` result. The adapter accepts AC agent output after provider/runtime
preambles and rejects extra result fields.

The temporary proxy bridge and Agent proxy variables were removed afterward.
The M3 gate is therefore open only for the durable approved AC egress path and
final deployment/security review; it is not blocked on the execution contract
or the real local smoke. Unit and HTTP fixtures cover cancellation,
deadline-driven `StopRun`, reconnect offsets, malformed frames, dropped
streams, redaction, RBAC, CSRF, and stable SSE errors.
