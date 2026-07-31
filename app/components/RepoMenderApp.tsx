"use client";

import type { FormEvent, ReactNode } from "react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { approvals, navigation, tasks, TaskRecord, Tone } from "../lib/data";

// React effects load same-origin SCM APIs after hydration, while Next navigation
// provides durable, shareable URLs for repository list and detail screens.

type DialogState = {
  title: string;
  body: string;
  action: string;
  danger?: boolean;
} | null;

type SCMConnection = {
  id: string;
  provider: "github" | "gitlab";
  name: string;
  status: string;
  lastSyncedAt?: string;
};

type ConnectedRepository = {
  id: string;
  connectionId: string;
  provider: "github" | "gitlab";
  providerRepositoryId: string;
  fullName: string;
  cloneUrl: string;
  webUrl: string;
  defaultBranch: string;
  visibility: string;
  archived: boolean;
  enabled: boolean;
  metadata?: Record<string, unknown>;
  lastSyncedAt: string;
};

const detailTabs = ["Summary", "Findings", "Agent activity", "Tests", "Artifacts", "Run details", "Audit history"];

function Status({ tone, children }: { tone: Tone; children: ReactNode }) {
  return <span className={`status status-${tone}`}>{children}</span>;
}

function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow: string;
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div>
        <div className="eyebrow">{eyebrow}</div>
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  );
}

function Metric({ label, value, note, positive }: { label: string; value: string; note: string; positive?: boolean }) {
  return (
    <article className="metric-card">
      <span>{label}</span>
      <strong>{value}</strong>
      <small className={positive ? "positive" : ""}>{note}</small>
    </article>
  );
}

function TaskTable({ rows, onOpen }: { rows: TaskRecord[]; onOpen: (route: string) => void }) {
  return (
    <div className="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Task</th>
            <th>Repository</th>
            <th>Status</th>
            <th>Agent</th>
            <th>Duration</th>
            <th>Updated</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((task) => (
            <tr key={`${task.type}-${task.id}`} onClick={() => onOpen(task.route)} tabIndex={0} onKeyDown={(event) => event.key === "Enter" && onOpen(task.route)}>
              <td>
                <strong>{task.id} · {task.type}</strong>
                <small>{task.title}</small>
              </td>
              <td>{task.repository}</td>
              <td><Status tone={task.tone}>{task.status}</Status></td>
              <td>{task.agent}</td>
              <td>{task.duration}</td>
              <td>{task.updated}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Dashboard({ navigate }: { navigate: (route: string) => void }) {
  return (
    <>
      <PageHeader
        eyebrow="Acme Engineering · Payments Platform"
        title="What needs your attention"
        description="Five approvals, two high-risk findings, and one unhealthy automation."
        actions={<button className="primary-button" onClick={() => navigate("/automations")}>Create automation</button>}
      />
      <section className="metric-grid" aria-label="Engineering metrics">
        <Metric label="Tasks this week" value="248" note="↑ 12.4% from last week" positive />
        <Metric label="Awaiting approval" value="5" note="2 high-risk requests" />
        <Metric label="Mean CI diagnosis" value="6m 42s" note="↓ 1m 08s from last week" positive />
        <Metric label="Repair success rate" value="83.6%" note="42 pull requests merged" positive />
      </section>
      <div className="dashboard-grid">
        <div>
          <section className="panel">
            <div className="panel-heading">
              <div><span className="eyebrow">Action queue</span><h2>Needs attention</h2></div>
              <button className="text-button" onClick={() => navigate("/approvals")}>View all</button>
            </div>
            <button className="attention-row" onClick={() => navigate("/reviews/1842")}>
              <i className="risk-bar risk-critical" />
              <span><strong>#1842 may create duplicate captures</strong><small>payments-api · Senior Go Reviewer · 94% confidence</small></span>
              <Status tone="critical">Critical</Status>
            </button>
            <button className="attention-row" onClick={() => navigate("/repairs/731")}>
              <i className="risk-bar risk-info" />
              <span><strong>Repair plan is ready for approval</strong><small>Issue #731 · 3 files · estimated $1.84</small></span>
              <Status tone="info">Approval</Status>
            </button>
            <button className="attention-row" onClick={() => navigate("/diagnostics/pg16")}>
              <i className="risk-bar risk-warning" />
              <span><strong>PostgreSQL 16 integration tests are failing</strong><small>Root cause reproduced in sandbox sbx_8f2</small></span>
              <Status tone="high">High</Status>
            </button>
          </section>
          <section className="panel section-gap">
            <div className="panel-heading"><div><span className="eyebrow">Execution</span><h2>Recent tasks</h2></div><button className="text-button" onClick={() => navigate("/tasks")}>All tasks</button></div>
            <TaskTable rows={tasks.slice(0, 4)} onOpen={navigate} />
          </section>
        </div>
        <aside>
          <section className="panel approval-summary">
            <div className="panel-heading"><div><span className="eyebrow">Human control</span><h2>Approvals</h2></div><strong>5</strong></div>
            {approvals.slice(0, 3).map((approval) => (
              <button className="mini-approval" key={approval.title} onClick={() => navigate("/approvals")}>
                <span><strong>{approval.title}</strong><small>{approval.context}</small></span>
                <Status tone={approval.tone}>{approval.risk}</Status>
              </button>
            ))}
          </section>
          <section className="panel section-gap health-card">
            <div className="panel-heading"><div><span className="eyebrow">Repository health</span><h2>payments-api</h2></div><button className="text-button" onClick={() => navigate("/repositories/payments-api")}>Open</button></div>
            <div className="health-visual"><div className="health-ring"><strong>79</strong><span>/ 100</span></div><div className="spark-bars">{[42, 51, 48, 58, 55, 66, 79].map((height, index) => <i key={index} style={{ height: `${height}%` }} />)}</div></div>
            <div className="warning-callout"><strong>Automation degraded</strong><span>nightly-dependency-scan failed three times due to provider rate limits.</span></div>
          </section>
        </aside>
      </div>
    </>
  );
}

function ListPage({
  type,
  title,
  description,
  navigate,
}: {
  type?: TaskRecord["type"];
  title: string;
  description: string;
  navigate: (route: string) => void;
}) {
  const [filter, setFilter] = useState("All");
  const rows = useMemo(() => type ? tasks.filter((task) => task.type === type) : tasks, [type]);
  const visibleRows = filter === "Needs attention" ? rows.filter((task) => !["Passed", "Patch verified"].includes(task.status)) : rows;

  return (
    <>
      <PageHeader eyebrow="Engineering" title={title} description={description} actions={<button className="secondary-button">Export report</button>} />
      <div className="filter-bar" aria-label={`${title} filters`}>
        {["All", "Needs attention", "Running", "Last 7 days"].map((item) => (
          <button key={item} className={filter === item ? "filter-chip active" : "filter-chip"} onClick={() => setFilter(item)}>{item}</button>
        ))}
      </div>
      <section className="panel">
        <div className="panel-heading"><div><span className="eyebrow">Live queue</span><h2>{visibleRows.length} tasks</h2></div><button className="text-button">Customize columns</button></div>
        <TaskTable rows={visibleRows} onOpen={navigate} />
      </section>
    </>
  );
}

function ReviewDetail({ openDialog }: { openDialog: (dialog: DialogState) => void }) {
  const [tab, setTab] = useState("Findings");
  return (
    <>
      <PageHeader
        eyebrow="Code review · payments-api · Pull request #1842"
        title="Prevent duplicate payment capture"
        description="fix/payment-capture-race → main · Ava Chen · 8f7c2a1"
        actions={<><button className="secondary-button">Open original PR</button><button className="secondary-button">Re-run</button><button className="primary-button" onClick={() => openDialog({ title: "Request agent repair", body: "Create a governed repair task from the selected critical finding. A repair plan will require approval before code changes begin.", action: "Create repair task" })}>Request agent fix</button></>}
      />
      <div className="detail-status"><Status tone="critical">Critical risk</Status><span>3 findings</span><span>6 changed files</span><span>94% confidence</span></div>
      <div className="tabs" role="tablist" aria-label="Review detail sections">
        {detailTabs.map((item) => <button key={item} role="tab" aria-selected={tab === item} className={tab === item ? "active" : ""} onClick={() => setTab(item)}>{item}{item === "Findings" ? " 3" : ""}</button>)}
      </div>
      {tab === "Findings" || tab === "Summary" ? (
        <section className="review-workspace">
          <aside className="file-pane">
            <div className="pane-title">Changed files</div>
            {["capture_service.go", "capture_service_test.go", "idempotency.go", "transaction.go", "routes.go"].map((file, index) => <button key={file} className={index === 0 ? "file-row active" : "file-row"}><i className={index === 0 ? "file-dot critical" : "file-dot"} />{file}</button>)}
          </aside>
          <article className="diff-pane">
            <div className="pane-toolbar"><strong>capture_service.go</strong><span>Unified diff</span><button className="text-button">Search code</button></div>
            <pre className="code-diff">
              <span>118  118   func (s *Service) Capture(ctx context.Context, req CaptureRequest) error {"{"}</span>
              <span className="removed">119    -  payment, err := s.repo.FindByID(ctx, req.PaymentID)</span>
              <span className="removed">120    -  if payment.Status == Captured {"{"} return nil {"}"}</span>
              <span className="added">     119 +  return s.repo.WithTransaction(ctx, func(tx Repository) error {"{"}</span>
              <span className="added">     120 +    payment, err := tx.FindByIDForUpdate(ctx, req.PaymentID)</span>
              <span className="finding-note"><strong>Critical · Concurrent state check is unprotected</strong>Two requests can observe a pending payment before either transaction commits.</span>
              <span className="added">     121 +    if payment.Status == Captured {"{"} return nil {"}"}</span>
              <span className="added">     122 +    return tx.MarkCaptured(ctx, payment.ID, req.IdempotencyKey)</span>
              <span className="added">     123 +  {"}"})</span>
            </pre>
          </article>
          <aside className="finding-pane">
            <div className="pane-title">Finding 1 / 3</div>
            <Status tone="critical">Critical</Status>
            <h2>Concurrent requests can create duplicate capture records</h2>
            <p>Two parallel capture requests can both pass the status check before the update is persisted.</p>
            <div className="evidence-block"><span>Evidence</span><p>The read, status check, and MarkCaptured call have no shared transaction boundary or row lock.</p></div>
            <div className="evidence-block"><span>Suggested repair</span><p>Add FOR UPDATE, a unique idempotency constraint, and transaction retry.</p></div>
            <div className="finding-actions"><button className="primary-button">Mark valid</button><button className="secondary-button">False positive</button><button className="secondary-button">Create issue</button></div>
          </aside>
        </section>
      ) : <DetailTabPlaceholder tab={tab} />}
    </>
  );
}

function DetailTabPlaceholder({ tab }: { tab: string }) {
  return (
    <section className="panel detail-placeholder">
      <span className="eyebrow">Review evidence</span>
      <h2>{tab}</h2>
      <p>This section is wired into the route and tab model. The next backend integration phase will stream AC run data, test evidence, artifacts, and audit events here.</p>
    </section>
  );
}

function DiagnosisDetail({ openDialog }: { openDialog: (dialog: DialogState) => void }) {
  const stages = ["Received", "Log analysis", "Environment ready", "Reproduced", "Root cause", "Patch generated", "Tests passed", "Awaiting approval"];
  return (
    <>
      <PageHeader eyebrow="CI diagnosis · payments-api · integration-tests" title="PostgreSQL 16 integration tests failed" description="PR #1842 · Commit 8f7c2a1 · integration-tests" actions={<><button className="secondary-button">Retry CI</button><button className="primary-button" onClick={() => openDialog({ title: "Create repair task", body: "The failure was reproduced in an isolated sandbox and the proposed patch passed 148 tests.", action: "Create task" })}>Create repair task</button></>} />
      <section className="panel">
        <div className="panel-heading"><div><span className="eyebrow">Execution evidence</span><h2>Diagnosis timeline</h2></div><Status tone="info">7 / 8 complete</Status></div>
        <div className="timeline">{stages.map((stage, index) => <div className={index < 7 ? "timeline-step done" : "timeline-step current"} key={stage}><i /><span>{stage}</span></div>)}</div>
      </section>
      <div className="diagnosis-grid section-gap">
        <div>
          <section className="panel">
            <div className="panel-heading"><div><span className="eyebrow">Raw logs</span><h2>TestCapturePaymentConcurrent</h2></div><button className="text-button">Search logs</button></div>
            <pre className="log-viewer">{`=== RUN   TestCapturePaymentConcurrent
2026-07-30T09:42:18Z  preparing PostgreSQL 16 sandbox
2026-07-30T09:42:21Z  running 2 concurrent capture requests
--- FAIL: TestCapturePaymentConcurrent (0.91s)
    expected one capture record, found 2
payment_id=pay_01JX4  requests=2  isolation=READ COMMITTED
goroutine 218: repo.FindByID() → status=pending
goroutine 219: repo.FindByID() → status=pending`}</pre>
          </section>
          <section className="panel section-gap">
            <div className="panel-heading"><div><span className="eyebrow">Verification</span><h2>Test results</h2></div><Status tone="success">148 passed</Status></div>
            {["Concurrent capture repeated 20 times", "payments-api/internal/capture", "integration-tests / PostgreSQL 16"].map((test) => <div className="test-row" key={test}><i /> <span>{test}</span><strong>Passed</strong></div>)}
          </section>
        </div>
        <aside className="panel root-cause">
          <span className="eyebrow">Root cause summary</span>
          <h2>Transaction isolation lets two capture requests observe stale state</h2>
          <p>This failure was introduced by the current PR and is not a flaky test. Sandbox sbx_8f2 reproduced it with PostgreSQL 16 under READ COMMITTED.</p>
          <dl>
            <div><dt>Evidence</dt><dd>No transaction boundary between read, status check, and write.</dd></div>
            <div><dt>Affected files</dt><dd>capture_service.go · idempotency.go</dd></div>
            <div><dt>Suggested fix</dt><dd>Row lock + unique idempotency constraint + retry.</dd></div>
            <div><dt>Execution</dt><dd>6m 42s · $1.84 · sandbox sbx_8f2</dd></div>
          </dl>
          <button className="primary-button full-button" onClick={() => openDialog({ title: "Approve and create repair task", body: "Approval will preserve the diagnosis evidence and start a governed issue-repair workflow.", action: "Approve task" })}>Approve repair task</button>
        </aside>
      </div>
    </>
  );
}

function RepairDetail({ openDialog }: { openDialog: (dialog: DialogState) => void }) {
  const [tab, setTab] = useState("Repair plan");
  const tabs = ["Repair plan", "Changes", "Tests", "Agent activity", "Artifacts", "Run details", "Audit history"];
  return (
    <>
      <PageHeader eyebrow="Issue repair · payments-api · #731" title="Retry webhook delivery with exponential backoff" description="fix/webhook-backoff · Owner: Li Wei" actions={<><button className="secondary-button">Stop</button><button className="secondary-button">Reassign</button><button className="secondary-button">Open issue</button></>} />
      <div className="detail-status"><Status tone="warning">Plan approval</Status><span>Medium risk</span><span>Estimated $1.84</span></div>
      <div className="tabs" role="tablist" aria-label="Repair detail sections">{tabs.map((item) => <button key={item} role="tab" aria-selected={tab === item} className={tab === item ? "active" : ""} onClick={() => setTab(item)}>{item}</button>)}</div>
      {tab === "Repair plan" ? (
        <div className="repair-grid">
          <section className="panel">
            <div className="panel-heading"><div><span className="eyebrow">Agent proposal</span><h2>Repair plan</h2></div><Status tone="warning">Approval required</Status></div>
            {[
              ["Map the existing retry lifecycle", "Confirm worker failures, idempotency behavior, and queue acknowledgement.", "Complete"],
              ["Define bounded exponential backoff", "Modify delivery_policy.go and document delay impact.", "Complete"],
              ["Implement retry classification and jitter", "Handle 429, 5xx, and network timeouts.", "Pending"],
              ["Run integration tests and create Draft PR", "Verify retry suite, regression tests, and queue visibility timeout.", "Pending"],
            ].map(([title, description, state], index) => (
              <div className="plan-row" key={title}><i className={state === "Complete" ? "complete" : ""}>{state === "Complete" ? "✓" : index + 1}</i><span><strong>{title}</strong><small>{description}</small></span><Status tone={state === "Complete" ? "success" : "info"}>{state}</Status></div>
            ))}
            <div className="panel-footer"><button className="secondary-button">Request changes</button><button className="primary-button" onClick={() => openDialog({ title: "Approve repair plan", body: "The agent will continue with retry classification, code changes, and the declared test plan.", action: "Approve plan" })}>Approve plan</button></div>
          </section>
          <aside>
            <section className="panel root-cause">
              <span className="eyebrow">Agent understanding</span>
              <h2>Use bounded, observable retries for transient network failures</h2>
              <p>Do not retry permanent 4xx responses. Preserve delivery idempotency and audit events.</p>
              <dl><div><dt>Acceptance criteria</dt><dd>5xx and timeouts retry up to five times with jitter.</dd></div><div><dt>Files</dt><dd>retry.go · worker.go · delivery_policy.go</dd></div><div><dt>Risk</dt><dd>Medium · throughput and retry queue behavior.</dd></div></dl>
            </section>
            <section className="panel section-gap"><div className="panel-heading"><div><span className="eyebrow">Validation</span><h2>Test plan</h2></div></div><div className="test-row"><i /><span>Existing webhook unit tests</span><strong>24 passed</strong></div><div className="test-row pending"><i /><span>Backoff integration suite</span><strong>Blocked</strong></div></section>
          </aside>
        </div>
      ) : <DetailTabPlaceholder tab={tab} />}
    </>
  );
}

function ApprovalsPage({ openDialog }: { openDialog: (dialog: DialogState) => void }) {
  const [filter, setFilter] = useState("All");
  const visible = filter === "High risk" ? approvals.filter((item) => item.tone === "critical") : approvals;
  return (
    <>
      <PageHeader eyebrow="Engineering · Human control" title="Approval center" description="Review high-impact agent actions with evidence, scope, and cost before execution." actions={<button className="secondary-button">Approval policy</button>} />
      <div className="filter-bar">{["All", "High risk", "Plans", "Patches", "Permissions"].map((item) => <button key={item} className={filter === item ? "filter-chip active" : "filter-chip"} onClick={() => setFilter(item)}>{item}</button>)}</div>
      <section className="approval-grid">
        {visible.map((approval) => (
          <article className="approval-card" key={approval.title}>
            <Status tone={approval.tone}>{approval.risk}</Status>
            <span className="eyebrow">{approval.context}</span>
            <h2>{approval.title}</h2>
            <p>{approval.summary}</p>
            <div className="approval-card-footer"><small>{approval.meta}</small><button className="primary-button" onClick={() => openDialog({ title: approval.title, body: `${approval.summary} This decision will be written to the audit log.`, action: approval.title.includes("network") ? "Allow for 15 minutes" : "Approve" })}>Review</button></div>
          </article>
        ))}
      </section>
    </>
  );
}

function csrfToken() {
  const value = document.cookie.split("; ").find((item) => item.startsWith("repomender_csrf="));
  return value ? decodeURIComponent(value.split("=").slice(1).join("=")) : "";
}

function RepositoriesPage({ navigate }: { navigate: (route: string) => void }) {
  const [connections, setConnections] = useState<SCMConnection[]>([]);
  const [connectedRepositories, setConnectedRepositories] = useState<ConnectedRepository[]>([]);
  const [providers, setProviders] = useState({ github: false, gitlab: false });
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState("");
  const [syncing, setSyncing] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setMessage("");
    try {
      const [connectionResponse, repositoryResponse, providerResponse] = await Promise.all([
        fetch("/api/v1/scm/connections"),
        fetch("/api/v1/repositories"),
        fetch("/api/v1/scm/providers"),
      ]);
      if ([connectionResponse, repositoryResponse, providerResponse].some((response) => response.status === 401)) {
        navigate("/login");
        return;
      }
      if (!connectionResponse.ok || !repositoryResponse.ok || !providerResponse.ok) {
        throw new Error("SCM API unavailable");
      }
      const [connectionData, repositoryData, providerData] = await Promise.all([
        connectionResponse.json(), repositoryResponse.json(), providerResponse.json(),
      ]);
      setConnections(connectionData.connections ?? []);
      setConnectedRepositories(repositoryData.repositories ?? []);
      setProviders({
        github: Boolean(providerData.providers?.github),
        gitlab: Boolean(providerData.providers?.gitlab),
      });
    } catch {
      setMessage("SCM connections are not enabled or the RepoMender API is unavailable.");
    } finally {
      setLoading(false);
    }
  }, [navigate]);

  useEffect(() => {
    const timer = window.setTimeout(() => void load(), 0);
    return () => window.clearTimeout(timer);
  }, [load]);

  const connect = (provider: "github" | "gitlab") => {
    window.location.assign(`/api/v1/scm/${provider}/connect?returnTo=/repositories`);
  };

  const sync = async (connection: SCMConnection) => {
    setSyncing(connection.id);
    setMessage("");
    try {
      const response = await fetch(`/api/v1/scm/connections/${connection.id}/sync`, {
        method: "POST",
        headers: { "X-CSRF-Token": csrfToken() },
      });
      if (!response.ok) throw new Error("sync failed");
      await load();
    } catch {
      setMessage(`Could not synchronize ${connection.name}. Check provider access and try again.`);
    } finally {
      setSyncing("");
    }
  };

  return (
    <>
      <PageHeader eyebrow="Assets · SCM" title="Repositories" description={providers.gitlab ? "GitHub and GitLab codebases synchronized through governed provider connections." : "GitHub repositories synchronized through a governed GitHub App connection."} actions={<div className="scm-actions">{providers.gitlab ? <button className="secondary-button" onClick={() => connect("gitlab")}>Connect GitLab</button> : null}<button className="primary-button" disabled={!providers.github} onClick={() => connect("github")}>Install GitHub App</button></div>} />
      {message ? <div className="identity-message scm-message" role="status">{message}</div> : null}
      <section className="panel scm-connections">
        <div className="panel-heading"><div><span className="eyebrow">Connections</span><h2>{connections.length} active provider {connections.length === 1 ? "connection" : "connections"}</h2></div><Status tone={connections.length ? "success" : "neutral"}>{connections.length ? "Connected" : "Setup required"}</Status></div>
        {connections.length ? <div className="connection-list">{connections.map((connection) => <div className="connection-row" key={connection.id}><span className="repo-mark">{connection.provider === "github" ? "GH" : "GL"}</span><span><strong>{connection.name}</strong><small>{connection.provider} · {connection.status} · {connection.lastSyncedAt ? `synced ${new Date(connection.lastSyncedAt).toLocaleString()}` : "not synchronized"}</small></span><button className="secondary-button" disabled={syncing === connection.id} onClick={() => void sync(connection)}>{syncing === connection.id ? "Syncing…" : "Sync now"}</button></div>)}</div> : <div className="empty-state"><strong>Connect a source provider</strong><span>{providers.gitlab ? "Configure GitHub App or GitLab OAuth credentials, then authorize a connection." : "Configure and install the RepoMender GitHub App to synchronize repositories."}</span></div>}
      </section>
      <section className="panel section-gap"><div className="panel-heading"><div><span className="eyebrow">Synchronized inventory</span><h2>{connectedRepositories.length} repositories</h2></div><button className="text-button" onClick={() => void load()}>Refresh inventory</button></div>
        {loading ? <div className="empty-state"><strong>Loading connected repositories…</strong></div> : null}
        {!loading && !connectedRepositories.length ? <div className="empty-state"><strong>No repositories synchronized</strong><span>Complete a provider connection or check its repository permissions.</span></div> : null}
        {!loading && connectedRepositories.length ? <div className="repository-grid">{connectedRepositories.map((repository) => <button className={`repository-card${repository.enabled ? "" : " repository-disabled"}`} key={repository.id} onClick={() => navigate(`/repositories/${encodeURIComponent(repository.id)}`)}><div><span className="repo-mark">{repository.provider === "github" ? "GH" : "GL"}</span><span><strong>{repository.fullName}</strong><small>{repository.provider} · {String(repository.metadata?.language || repository.visibility)}</small></span></div><div className="repo-stats"><span><small>Default branch</small><strong>{repository.defaultBranch || "Not set"}</strong></span><span><small>Visibility</small><strong>{repository.visibility}</strong></span><Status tone={repository.enabled ? "success" : "warning"}>{repository.enabled ? "Enabled" : "Access removed"}</Status></div></button>)}</div> : null}
      </section>
    </>
  );
}

function RepositoryDetail({ id, navigate }: { id: string; navigate: (route: string) => void }) {
  const [repository, setRepository] = useState<ConnectedRepository | null>(null);
  const [message, setMessage] = useState("");

  useEffect(() => {
    const load = async () => {
      try {
        const response = await fetch(`/api/v1/repositories/${encodeURIComponent(id)}`);
        if (response.status === 401) {
          navigate("/login");
          return;
        }
        if (!response.ok) throw new Error("repository unavailable");
        const data = await response.json();
        setRepository(data.repository);
      } catch {
        setMessage("This repository is unavailable or has not been synchronized.");
      }
    };
    const timer = window.setTimeout(() => void load(), 0);
    return () => window.clearTimeout(timer);
  }, [id, navigate]);

  if (message) {
    return <><PageHeader eyebrow="Repository" title="Repository unavailable" description={message} actions={<button className="secondary-button" onClick={() => navigate("/repositories")}>Back to repositories</button>} /></>;
  }
  if (!repository) {
    return <><PageHeader eyebrow="Repository" title="Loading repository…" description="Reading the synchronized SCM inventory." /></>;
  }
  const language = String(repository.metadata?.language || "Not reported");
  return (
    <>
      <PageHeader eyebrow={`Repository · ${repository.provider}`} title={repository.fullName} description={`${repository.webUrl} · ${language} · ${repository.defaultBranch || "No default branch"}`} actions={<><button className="secondary-button" onClick={() => navigate("/repositories")}>Back</button><a className="primary-button link-button" href={repository.webUrl} target="_blank" rel="noreferrer">Open in {repository.provider === "github" ? "GitHub" : "GitLab"}</a></>} />
      <section className="metric-grid"><Metric label="Provider" value={repository.provider === "github" ? "GitHub" : "GitLab"} note="Connected SCM" /><Metric label="Default branch" value={repository.defaultBranch || "—"} note="Provider source of truth" /><Metric label="Visibility" value={repository.visibility} note={repository.archived ? "Archived" : "Active repository"} /><Metric label="Access" value={repository.enabled ? "Enabled" : "Removed"} note={`Synced ${new Date(repository.lastSyncedAt).toLocaleString()}`} positive={repository.enabled} /></section>
      <div className="dashboard-grid section-gap"><section className="panel"><div className="panel-heading"><div><span className="eyebrow">Source inventory</span><h2>Repository identity</h2></div></div><dl className="repository-facts"><div><dt>Repository ID</dt><dd>{repository.id}</dd></div><div><dt>Provider repository ID</dt><dd>{repository.providerRepositoryId}</dd></div><div><dt>Clone URL</dt><dd>{repository.cloneUrl}</dd></div><div><dt>Connection ID</dt><dd>{repository.connectionId}</dd></div></dl></section><aside className="panel root-cause"><span className="eyebrow">M2 boundary</span><h2>Connection verified; execution remains disabled</h2><p>Repository access and webhook delivery are available. Reviews, CI diagnosis, and repairs remain behind later module gates.</p><dl><div><dt>Direct branch writes</dt><dd>Disabled</dd></div><div><dt>Automatic merge</dt><dd>Disabled</dd></div><div><dt>Credential storage</dt><dd>Encrypted or short-lived</dd></div></dl></aside></div>
    </>
  );
}

function AutomationsPage({ showToast }: { showToast: (message: string) => void }) {
  const [step, setStep] = useState(0);
  const steps = ["Basic information", "Trigger", "Execution", "Policy & approval", "Notification & output"];
  return (
    <>
      <PageHeader eyebrow="Automation · Editor" title="PR code review" description="Configure triggers, agent execution, and enterprise guardrails." actions={<><button className="secondary-button" onClick={() => showToast("Draft saved locally")}>Save draft</button><button className="primary-button" onClick={() => showToast("Automation published")}>Publish automation</button></>} />
      <div className="automation-layout">
        <aside className="wizard-nav">{steps.map((item, index) => <button key={item} className={step === index ? "active" : ""} onClick={() => setStep(index)}><i>{index + 1}</i>{item}</button>)}</aside>
        <section className="panel automation-form">
          <div className="panel-heading"><div><span className="eyebrow">Step {step + 1} of 5</span><h2>{steps[step]}</h2></div><button className="secondary-button">YAML mode</button></div>
          {step === 0 ? <div className="form-grid"><label className="full-field">Automation name<input defaultValue="PR Code Review · Payments" /></label><label className="full-field">Description<input defaultValue="Multi-agent review for Payments Platform pull requests." /></label><label>Repository scope<select defaultValue="payments-api"><option>payments-api</option><option>All Payments repositories</option></select></label><label>Agent template<select defaultValue="Senior Go Reviewer v4.2"><option>Senior Go Reviewer v4.2</option><option>Security Reviewer v2.8</option></select></label></div> : null}
          {step === 1 ? <div className="form-grid"><label>Event<select><option>Pull request opened or updated</option><option>Push to protected branch</option></select></label><label>Target branches<input defaultValue="main, release/*" /></label><label className="full-field">Ignored paths<input defaultValue="docs/**, generated/**" /></label></div> : null}
          {step === 2 ? <div className="form-grid"><label>Model<select><option>GPT-5.4</option><option>Claude Sonnet</option></select></label><label>Timeout<select><option>20 minutes</option><option>40 minutes</option></select></label><label>Concurrency<input type="number" defaultValue="3" /></label><label>Retry policy<select><option>Twice on infrastructure failure</option></select></label></div> : null}
          {step === 3 ? <div className="form-grid"><label>Policy set<select><option>Payments Standard Guardrails</option></select></label><label>Approval rule<select><option>Critical and high patches</option></select></label><label className="toggle-field full-field"><span><strong>Allow suggested patches</strong><small>Agent may prepare a patch but cannot merge it.</small></span><input type="checkbox" defaultChecked /></label><label className="toggle-field full-field"><span><strong>External network</strong><small>Require approval for destinations outside the allowlist.</small></span><input type="checkbox" /></label></div> : null}
          {step === 4 ? <div className="form-grid"><label>Slack channel<input defaultValue="#payments-engineering" /></label><label>Output action<select><option>Post review summary</option><option>Create repair task</option></select></label><label className="toggle-field full-field"><span><strong>Create repair task for critical findings</strong><small>Only when confidence is at least 90%.</small></span><input type="checkbox" defaultChecked /></label></div> : null}
          <div className="panel-footer"><button className="secondary-button" disabled={step === 0} onClick={() => setStep((current) => Math.max(0, current - 1))}>Back</button><button className="primary-button" disabled={step === steps.length - 1} onClick={() => setStep((current) => Math.min(steps.length - 1, current + 1))}>Continue</button></div>
        </section>
      </div>
    </>
  );
}

function RunsPage({ navigate }: { navigate: (route: string) => void }) {
  return <ListPage title="Runs" description="Execution history mapped to AC runs and isolated sandboxes." navigate={navigate} />;
}

function SettingsPage() {
  return (
    <>
      <PageHeader eyebrow="Platform administration" title="Settings" description="Integrations, models, runtimes, access, and governance." />
      <section className="settings-grid">{[
        ["Source integrations", "GitHub Enterprise connected · 12 repositories", "Healthy"],
        ["Models & providers", "OpenAI and Anthropic · automatic fallback enabled", "2 providers"],
        ["Runtime environments", "Docker healthy · BoxLite unavailable on this host", "1 warning"],
        ["Secrets", "8 scoped credentials · no expiring credentials", "Protected"],
        ["Members & roles", "34 members · 5 custom roles", "SSO enforced"],
        ["Audit & retention", "365-day event retention · export enabled", "Active"],
      ].map(([title, description, state]) => <button className="settings-card" key={title}><span><strong>{title}</strong><small>{description}</small></span><Status tone={state.includes("warning") ? "warning" : "neutral"}>{state}</Status></button>)}</section>
    </>
  );
}

function IdentityPage({ navigate }: { navigate: (route: string) => void }) {
  const [mode, setMode] = useState<"login" | "bootstrap">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setPending(true);
    setMessage("");
    try {
      const response = await fetch(`/api/v1/auth/${mode}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      if (!response.ok) {
        const result = await response.json().catch(() => ({ error: "request_failed" }));
        setMessage(result.error === "bootstrap_unavailable"
          ? "An administrator already exists. Sign in instead."
          : "The credentials could not be accepted.");
        return;
      }
      if (mode === "bootstrap") {
        setMode("login");
        setPassword("");
        setMessage("Administrator created. Sign in to continue.");
        return;
      }
      navigate("/");
    } catch {
      setMessage("RepoMender API is unavailable. Check the self-hosted stack and try again.");
    } finally {
      setPending(false);
    }
  };

  return (
    <main className="identity-shell">
      <section className="identity-card">
        <div className="identity-brand"><i className="brand-mark" /><strong>RepoMender</strong></div>
        <span className="eyebrow">Enterprise access</span>
        <h1>{mode === "login" ? "Sign in to your control plane" : "Create the bootstrap administrator"}</h1>
        <p>{mode === "login"
          ? "Use enterprise SSO or the emergency local administrator account."
          : "This one-time path closes permanently after the first administrator is created."}</p>
        <button className="secondary-button oidc-button" onClick={() => window.location.assign("/api/v1/auth/oidc/start")}>
          Continue with enterprise SSO
        </button>
        <div className="identity-divider"><span>or use local fallback</span></div>
        <form onSubmit={submit}>
          <label>Email<input type="email" autoComplete="username" required value={email} onChange={(event) => setEmail(event.target.value)} /></label>
          <label>Password<input type="password" autoComplete={mode === "login" ? "current-password" : "new-password"} minLength={12} required value={password} onChange={(event) => setPassword(event.target.value)} /></label>
          {message ? <div className="identity-message" role="status">{message}</div> : null}
          <button className="primary-button full-button" disabled={pending}>{pending ? "Please wait…" : mode === "login" ? "Sign in" : "Create administrator"}</button>
        </form>
        <button className="text-button identity-mode" onClick={() => { setMode(mode === "login" ? "bootstrap" : "login"); setMessage(""); }}>
          {mode === "login" ? "First installation? Create administrator" : "Administrator already exists? Sign in"}
        </button>
      </section>
      <aside className="identity-context">
        <span className="eyebrow">Governed engineering automation</span>
        <h2>Review, diagnose, and repair without surrendering control.</h2>
        <ul>
          <li><strong>Isolated execution</strong><span>Agent Compose keeps every task in a governed sandbox.</span></li>
          <li><strong>Human approval</strong><span>High-impact actions pause before code or permissions change.</span></li>
          <li><strong>Complete evidence</strong><span>Runs, findings, tests, and decisions remain auditable.</span></li>
        </ul>
      </aside>
    </main>
  );
}

export function RepoMenderApp() {
  const pathname = usePathname();
  const router = useRouter();
  const [theme, setTheme] = useState<"light" | "dark">("light");
  const [mobileNav, setMobileNav] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  const [dialog, setDialog] = useState<DialogState>(null);
  const [toastMessage, setToastMessage] = useState("");

  // Theme and keyboard preferences are device-local; business state will come from
  // RepoMender APIs rather than browser storage when the AC integration is added.
  useEffect(() => {
    const stored = window.localStorage.getItem("repomender-theme");
    const themeTimer = window.setTimeout(() => {
      if (stored === "dark") setTheme("dark");
    }, 0);
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setCommandOpen(true);
      }
      if (event.key === "Escape") {
        setCommandOpen(false);
        setDialog(null);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.clearTimeout(themeTimer);
      window.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  const navigate = useCallback((route: string) => {
    router.push(route);
    setCommandOpen(false);
    setMobileNav(false);
  }, [router]);

  const showToast = (message: string) => {
    setToastMessage(message);
    window.setTimeout(() => setToastMessage(""), 2600);
  };

  const toggleTheme = () => {
    const next = theme === "light" ? "dark" : "light";
    setTheme(next);
    window.localStorage.setItem("repomender-theme", next);
  };

  const routeContent = () => {
    if (pathname === "/login") return <IdentityPage navigate={navigate} />;
    if (pathname === "/") return <Dashboard navigate={navigate} />;
    if (pathname === "/tasks") return <ListPage title="All tasks" description="Unified reviews, diagnostics, repairs, and future maintenance automations." navigate={navigate} />;
    if (pathname === "/reviews") return <ListPage type="Code Review" title="Code reviews" description="Pull request risk, findings, evidence, and suggested repairs." navigate={navigate} />;
    if (pathname.startsWith("/reviews/")) return <ReviewDetail openDialog={setDialog} />;
    if (pathname === "/diagnostics") return <ListPage type="CI Diagnosis" title="CI diagnostics" description="Reproduced failures, root causes, patches, and verification evidence." navigate={navigate} />;
    if (pathname.startsWith("/diagnostics/")) return <DiagnosisDetail openDialog={setDialog} />;
    if (pathname === "/repairs") return <ListPage type="Issue Repair" title="Issue repairs" description="Governed issue-to-pull-request workflows with human approval." navigate={navigate} />;
    if (pathname.startsWith("/repairs/")) return <RepairDetail openDialog={setDialog} />;
    if (pathname === "/approvals") return <ApprovalsPage openDialog={setDialog} />;
    if (pathname === "/repositories") return <RepositoriesPage navigate={navigate} />;
    if (pathname.startsWith("/repositories/")) return <RepositoryDetail id={decodeURIComponent(pathname.slice("/repositories/".length))} navigate={navigate} />;
    if (pathname === "/automations") return <AutomationsPage showToast={showToast} />;
    if (pathname === "/runs") return <RunsPage navigate={navigate} />;
    if (pathname === "/settings") return <SettingsPage />;
    return <Dashboard navigate={navigate} />;
  };

  if (pathname === "/login") {
    return <div className="app-shell identity-app" data-theme={theme}><IdentityPage navigate={navigate} /></div>;
  }

  return (
    <div className="app-shell" data-theme={theme}>
      <aside className={mobileNav ? "sidebar open" : "sidebar"}>
        <button className="brand" onClick={() => navigate("/")} aria-label="RepoMender dashboard"><i className="brand-mark" /><strong>RepoMender</strong></button>
        <button className="organization-switcher"><span className="organization-avatar">AE</span><span><strong>Acme Engineering</strong><small>Payments Platform</small></span><span aria-hidden>⌄</span></button>
        <nav aria-label="Primary navigation">
          {navigation.map((group) => (
            <div className="nav-group" key={group.label}><div className="nav-label">{group.label}</div>{group.items.map((item) => {
              const active = item.route === "/" ? pathname === "/" : pathname.startsWith(item.route);
              return <button key={item.route} className={active ? "nav-item active" : "nav-item"} onClick={() => navigate(item.route)}><i>{item.glyph}</i><span>{item.label}</span>{item.count ? <b>{item.count}</b> : null}</button>;
            })}</div>
          ))}
        </nav>
        <div className="sidebar-footer"><span className="environment-dot" /><span><strong>AC control plane</strong><small>Healthy · v0.7 preview</small></span></div>
      </aside>
      <section className="workspace">
        <header className="topbar">
          <button className="mobile-menu" onClick={() => setMobileNav((open) => !open)} aria-label="Toggle navigation">☰</button>
          <button className="command-trigger" onClick={() => setCommandOpen(true)}><span>⌕</span><span>Search tasks, repositories, PRs, or commands</span><kbd>⌘ K</kbd></button>
          <div className="topbar-actions"><button className="icon-button" onClick={toggleTheme} aria-label={`Switch to ${theme === "light" ? "dark" : "light"} theme`}>{theme === "light" ? "◐" : "☀"}</button><button className="icon-button" aria-label="Notifications">◇<i className="notification-dot" /></button><button className="user-button" onClick={() => navigate("/login")} aria-label="Open sign-in and account page">LW</button></div>
        </header>
        <main>{routeContent()}</main>
        <nav className="mobile-tabs" aria-label="Mobile navigation"><button className={pathname === "/" ? "active" : ""} onClick={() => navigate("/")}>Overview</button><button className={pathname.startsWith("/reviews") ? "active" : ""} onClick={() => navigate("/reviews")}>Reviews</button><button className={pathname.startsWith("/approvals") ? "active" : ""} onClick={() => navigate("/approvals")}>Approvals</button><button className={pathname.startsWith("/tasks") ? "active" : ""} onClick={() => navigate("/tasks")}>Tasks</button></nav>
      </section>
      {commandOpen ? <CommandPalette navigate={navigate} onClose={() => setCommandOpen(false)} /> : null}
      {dialog ? <ApprovalDialog dialog={dialog} onClose={() => setDialog(null)} onConfirm={() => { setDialog(null); showToast(`${dialog.action} recorded in the audit log`); }} /> : null}
      <div className={toastMessage ? "toast show" : "toast"} role="status">{toastMessage}</div>
    </div>
  );
}

function CommandPalette({ navigate, onClose }: { navigate: (route: string) => void; onClose: () => void }) {
  const [query, setQuery] = useState("");
  const commands = [
    ["Open PR #1842 code review", "/reviews/1842"],
    ["Open PostgreSQL 16 CI diagnosis", "/diagnostics/pg16"],
    ["Review Issue #731 repair plan", "/repairs/731"],
    ["Process approval requests", "/approvals"],
    ["Browse connected repositories", "/repositories"],
  ];
  const filtered = commands.filter(([label]) => label.toLowerCase().includes(query.toLowerCase()));
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (filtered[0]) navigate(filtered[0][1]);
  };
  return <div className="modal-backdrop" onMouseDown={(event) => event.currentTarget === event.target && onClose()}><section className="command-palette" role="dialog" aria-modal="true" aria-labelledby="command-title"><div className="dialog-heading"><h2 id="command-title">Quick navigation</h2><button className="icon-button" onClick={onClose} aria-label="Close command palette">×</button></div><form onSubmit={submit}><input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search tasks, repositories, or commands…" aria-label="Search commands" /></form><div className="command-results">{filtered.map(([label, route]) => <button key={route} onClick={() => navigate(route)}><span>{label}</span><kbd>↵</kbd></button>)}</div></section></div>;
}

function ApprovalDialog({ dialog, onClose, onConfirm }: { dialog: Exclude<DialogState, null>; onClose: () => void; onConfirm: () => void }) {
  return <div className="modal-backdrop" onMouseDown={(event) => event.currentTarget === event.target && onClose()}><section className="approval-dialog" role="dialog" aria-modal="true" aria-labelledby="approval-dialog-title"><div className="dialog-heading"><div><span className="eyebrow">Human approval</span><h2 id="approval-dialog-title">{dialog.title}</h2></div><button className="icon-button" onClick={onClose} aria-label="Close approval dialog">×</button></div><div className="dialog-body"><div className="warning-callout"><strong>Requested action</strong><span>{dialog.body}</span></div><dl><div><dt>Repository</dt><dd>payments-api</dd></div><div><dt>Agent</dt><dd>Senior Go Repair</dd></div><div><dt>Estimated cost</dt><dd>$1.84</dd></div></dl></div><div className="dialog-actions"><button className="secondary-button" onClick={onClose}>Request changes</button><button className="danger-button" onClick={onClose}>Reject</button><button className="primary-button" onClick={onConfirm}>{dialog.action}</button></div></section></div>;
}
