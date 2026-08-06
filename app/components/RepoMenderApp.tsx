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

type APITask = {
  id: string;
  kind: "code_review" | "ci_diagnosis" | "issue_repair";
  status: string;
  title: string;
  repositoryName: string;
  attempts: number;
  maxAttempts: number;
  createdAt: string;
  updatedAt: string;
  lastError?: string;
};

type APIApproval = {
  id: string;
  action: "repair_plan" | "publish_patch" | "sandbox_network";
  state: "pending" | "approved" | "rejected" | "expired" | "cancelled" | "consumed";
  risk: "low" | "medium" | "high" | "critical";
  actionDigest: string;
  requesterId: string;
  eligibleRoles: string[];
  requestedAt: string;
  expiresAt: string;
  decisionReason?: string;
  metadata?: Record<string, unknown>;
};

function taskKindLabel(kind: APITask["kind"]): TaskRecord["type"] {
  if (kind === "code_review") return "Code Review";
  if (kind === "ci_diagnosis") return "CI Diagnosis";
  return "Issue Repair";
}

function taskTone(status: string): Tone {
  if (status === "succeeded") return "success";
  if (status === "failed") return "critical";
  if (status === "cancelled" || status === "superseded") return "neutral";
  if (status === "awaiting_approval") return "warning";
  if (status === "running") return "info";
  return "neutral";
}

function taskStatusLabel(status: string): string {
  return status.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function TasksPage({ navigate, kindFilter }: { navigate: (route: string) => void; kindFilter?: APITask["kind"] }) {
  const [items, setItems] = useState<APITask[]>([]);
  const [filter, setFilter] = useState("All");
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setMessage("");
    try {
      const query = kindFilter ? `&kind=${encodeURIComponent(kindFilter)}` : "";
      const response = await fetch(`/api/v1/tasks?limit=100${query}`);
      if (response.status === 401) {
        navigate("/login");
        return;
      }
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        setMessage(response.status === 404 ? "M4 task management is disabled on this deployment." : `Task API unavailable: ${body.error || "request_failed"}`);
        return;
      }
      setItems((body.tasks ?? []) as APITask[]);
    } catch {
      setMessage("RepoMender API is unavailable. Task data was not replaced with browser mocks.");
    } finally {
      setLoading(false);
    }
  }, [kindFilter, navigate]);

  useEffect(() => {
    const timer = window.setTimeout(() => void load(), 0);
    return () => window.clearTimeout(timer);
  }, [load]);

  const visible = filter === "Needs attention"
    ? items.filter((item) => !["succeeded", "cancelled", "superseded"].includes(item.status))
    : filter === "Running"
      ? items.filter((item) => item.status === "running")
      : items;
	const rows: TaskRecord[] = visible.map((item) => ({
    id: item.id.slice(0, 12),
    type: taskKindLabel(item.kind),
    title: item.title,
    repository: item.repositoryName || "—",
    status: taskStatusLabel(item.status),
    tone: taskTone(item.status),
    agent: `Attempt ${item.attempts}/${item.maxAttempts}`,
    duration: "—",
    updated: new Date(item.updatedAt).toLocaleString(),
		route: `${kindFilter === "code_review" ? "/reviews" : kindFilter === "ci_diagnosis" ? "/diagnostics" : "/tasks"}/${encodeURIComponent(item.id)}`,
	}));
	const moduleLabel = kindFilter === "code_review" ? "M5 code review" : kindFilter === "ci_diagnosis" ? "M6 CI diagnosis" : "M4 task core";
	const pageTitle = kindFilter === "code_review" ? "Code reviews" : kindFilter === "ci_diagnosis" ? "CI diagnostics" : "All tasks";
	const pageDescription = kindFilter === "code_review" ? "GitHub pull request reviews, findings, evidence, and immutable commit runs." : kindFilter === "ci_diagnosis" ? "Failed GitHub Actions runs, redacted logs, root-cause hypotheses, and reproduction evidence." : "Durable reviews, diagnostics, repairs, retries, and audit-linked runs.";

  return (
    <>
	<PageHeader eyebrow={`Engineering · ${moduleLabel}`} title={pageTitle} description={pageDescription} actions={<button className="secondary-button" onClick={() => void load()}>Refresh</button>} />
      {message ? <div className="identity-message" role="status">{message}</div> : null}
      <div className="filter-bar" aria-label="Task filters">
        {["All", "Needs attention", "Running"].map((item) => <button key={item} className={filter === item ? "filter-chip active" : "filter-chip"} onClick={() => setFilter(item)}>{item}</button>)}
      </div>
      <section className="panel">
        <div className="panel-heading"><div><span className="eyebrow">Persistent queue</span><h2>{loading ? "Loading tasks…" : `${rows.length} tasks`}</h2></div><span className="eyebrow">PostgreSQL-backed</span></div>
        {!loading && rows.length ? <TaskTable rows={rows} onOpen={navigate} /> : <div className="empty-state"><strong>{loading ? "Reading durable task state…" : "No tasks found"}</strong><span>{message || "Tasks created by GitHub triggers and governed operators will appear here."}</span></div>}
      </section>
    </>
  );
}

type TaskDetailResponse = {
  task: APITask & { payload?: Record<string, unknown>; sourceKey?: string; lastError?: string };
  runs: Array<{ id: string; attempt: number; status: string; correlationId: string; createdAt: string; finishedAt?: string }>;
  audit: Array<{ action: string; outcome: string; createdAt: string }>;
  findings?: Array<{ id: string; severity: string; category: string; path: string; lineStart?: number; explanation: string; remediation?: string }>;
  evidence?: Array<{ id: string; kind: string; title: string; content: unknown }>;
};

function TaskDetailPage({ id, navigate, backRoute = "/tasks" }: { id: string; navigate: (route: string) => void; backRoute?: string }) {
  const [detail, setDetail] = useState<TaskDetailResponse | null>(null);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState(false);

  const load = useCallback(async () => {
    try {
      const response = await fetch(`/api/v1/tasks/${encodeURIComponent(id)}`);
      if (response.status === 401) {
        navigate("/login");
        return;
      }
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || "task_unavailable");
      setDetail(body as TaskDetailResponse);
    } catch (error) {
      setMessage(`Task unavailable: ${error instanceof Error ? error.message : "request_failed"}`);
    }
  }, [id, navigate]);

  useEffect(() => {
    const timer = window.setTimeout(() => void load(), 0);
    return () => window.clearTimeout(timer);
  }, [load]);

  const mutate = async (action: "cancel" | "retry") => {
    setPending(true);
    setMessage("");
    try {
      const response = await fetch(`/api/v1/tasks/${encodeURIComponent(id)}/${action}`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfCookie() },
        body: JSON.stringify({ reason: `operator requested ${action} from task detail` }),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `${action}_failed`);
      await load();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : `${action}_failed`);
    } finally {
      setPending(false);
    }
  };

  if (message && !detail) return <><PageHeader eyebrow="Task" title="Task unavailable" description={message} actions={<button className="secondary-button" onClick={() => navigate(backRoute)}>Back</button>} /></>;
  if (!detail) return <PageHeader eyebrow="M4 task" title="Loading task…" description="Reading the durable task, runs, and audit history." />;
  const task = detail.task;
  return (
    <>
      <PageHeader eyebrow={`${taskKindLabel(task.kind)} · ${task.repositoryName || "repository not set"}`} title={task.title} description={`Task ${task.id} · source ${task.sourceKey || "not provided"}`} actions={<><button className="secondary-button" onClick={() => navigate(backRoute)}>Back</button>{task.status === "failed" || task.status === "cancelled" ? <button className="secondary-button" disabled={pending} onClick={() => void mutate("retry")}>Retry</button> : null}{["queued", "running", "awaiting_approval"].includes(task.status) ? <button className="danger-button" disabled={pending} onClick={() => void mutate("cancel")}>Cancel</button> : null}</>} />
      {message ? <div className="identity-message" role="status">{message}</div> : null}
      <div className="detail-status"><Status tone={taskTone(task.status)}>{taskStatusLabel(task.status)}</Status><span>{task.attempts}/{task.maxAttempts} attempts</span><span>Updated {new Date(task.updatedAt).toLocaleString()}</span></div>
      <div className="dashboard-grid">
        <section className="panel"><div className="panel-heading"><div><span className="eyebrow">Runs</span><h2>{detail.runs.length} durable runs</h2></div></div>{detail.runs.length ? detail.runs.map((run) => <div className="test-row" key={run.id}><i /><span><strong>{run.id.slice(0, 12)}</strong><small>Attempt {run.attempt} · {new Date(run.createdAt).toLocaleString()}</small></span><Status tone={taskTone(run.status)}>{taskStatusLabel(run.status)}</Status></div>) : <div className="empty-state"><strong>No run claimed yet</strong><span>The task remains in the persistent queue.</span></div>}</section>
        <aside className="panel"><div className="panel-heading"><div><span className="eyebrow">Audit history</span><h2>{detail.audit.length} events</h2></div></div>{detail.audit.length ? detail.audit.map((event, index) => <div className="test-row" key={`${event.action}-${index}`}><i /><span><strong>{event.action}</strong><small>{new Date(event.createdAt).toLocaleString()}</small></span><Status tone={event.outcome === "accepted" ? "success" : "neutral"}>{event.outcome}</Status></div>) : <div className="empty-state"><strong>No audit events</strong></div>}</aside>
      </div>
	      {task.kind === "code_review" ? <section className="panel section-gap"><div className="panel-heading"><div><span className="eyebrow">M5 review output</span><h2>{detail.findings?.length ?? 0} findings</h2></div><span className="eyebrow">Immutable commit: {String(task.payload?.headSHA || "not available").slice(0, 12)}</span></div>{detail.findings?.length ? detail.findings.map((finding) => <div className="test-row" key={finding.id}><i /><span><strong>{finding.severity} · {finding.path}{finding.lineStart ? `:${finding.lineStart}` : ""}</strong><small>{finding.explanation}</small></span><Status tone={finding.severity === "critical" || finding.severity === "high" ? "critical" : "warning"}>{finding.category}</Status></div>) : <div className="empty-state"><strong>No findings persisted</strong><span>AC has not completed a structured review for this task yet.</span></div>}</section> : null}
	      {task.kind === "ci_diagnosis" ? <section className="panel section-gap"><div className="panel-heading"><div><span className="eyebrow">M6 diagnosis output</span><h2>{detail.findings?.length ?? 0} root-cause hypotheses</h2></div><span className="eyebrow">Immutable commit: {String(task.payload?.headSHA || "not available").slice(0, 12)}</span></div>{detail.findings?.length ? detail.findings.map((finding) => <div className="test-row" key={finding.id}><i /><span><strong>{finding.explanation}</strong><small>{finding.remediation || "No recommended next action recorded."}</small></span><Status tone={finding.severity === "high" ? "critical" : "warning"}>{finding.category}</Status></div>) : <div className="empty-state"><strong>No diagnosis hypotheses persisted</strong><span>AC has not completed a structured CI diagnosis for this task yet.</span></div>}{detail.evidence?.length ? <div className="evidence-list">{detail.evidence.filter((item) => item.kind === "ci_log").slice(0, 20).map((item) => <pre className="log-viewer" key={item.id}>{JSON.stringify(item.content, null, 2)}</pre>)}</div> : null}</section> : null}
    </>
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

// Kept as a visual fallback for design review while the API-backed task detail
// route is enabled by the M5 feature flag.
// eslint-disable-next-line @typescript-eslint/no-unused-vars
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

// eslint-disable-next-line @typescript-eslint/no-unused-vars
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

function approvalLabel(action: APIApproval["action"]): string {
  if (action === "repair_plan") return "Repair plan";
  if (action === "publish_patch") return "Publish patch";
  return "Sandbox network";
}

function approvalTone(risk: APIApproval["risk"]): Tone {
  if (risk === "critical") return "critical";
  if (risk === "high") return "high";
  if (risk === "medium") return "warning";
  return "info";
}

function ApprovalsPage() {
  const [items, setItems] = useState<APIApproval[]>([]);
  const [filter, setFilter] = useState("All");
  const [selected, setSelected] = useState<APIApproval | null>(null);
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setMessage("");
    try {
      const response = await fetch("/api/v1/approvals?limit=100");
      const body = await response.json().catch(() => ({}));
      if (response.status === 401) {
        window.location.assign("/login");
        return;
      }
      if (!response.ok) {
        setMessage(response.status === 404 ? "M7 approval governance is disabled on this deployment." : `Approval API unavailable: ${body.error || "request_failed"}`);
        return;
      }
      setItems((body.approvals ?? []) as APIApproval[]);
    } catch {
      setMessage("RepoMender API is unavailable. Approval state was not replaced with browser mocks.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => void load(), 0);
    return () => window.clearTimeout(timer);
  }, [load]);

  const visible = items.filter((item) => {
    if (filter === "High risk") return item.risk === "high" || item.risk === "critical";
    if (filter === "Plans") return item.action === "repair_plan";
    if (filter === "Patches") return item.action === "publish_patch";
    if (filter === "Permissions") return item.action === "sandbox_network";
    return true;
  });

  const decide = async (item: APIApproval, decision: "approved" | "rejected") => {
    setMessage("");
    try {
      const response = await fetch(`/api/v1/approvals/${encodeURIComponent(item.id)}/decision`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
        body: JSON.stringify({ decision, reason: `operator selected ${decision} in approval inbox`, idempotencyKey: window.crypto.randomUUID() }),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        setMessage(`Decision rejected: ${body.error || "approval_decision_failed"}`);
        return;
      }
      const updated = body.approval as APIApproval;
      setItems((current) => current.map((currentItem) => currentItem.id === updated.id ? updated : currentItem));
      setSelected(updated);
    } catch {
      setMessage("RepoMender API is unavailable. The approval was not changed.");
    }
  };

  return (
    <>
      <PageHeader eyebrow="Engineering · Human control" title="Approval center" description="Review high-impact agent actions with evidence, scope, and cost before execution." actions={<button className="secondary-button" onClick={() => void load()}>Refresh</button>} />
      <div className="filter-bar">{["All", "High risk", "Plans", "Patches", "Permissions"].map((item) => <button key={item} className={filter === item ? "filter-chip active" : "filter-chip"} onClick={() => setFilter(item)}>{item}</button>)}</div>
      {message ? <div className="identity-message" role="status">{message}</div> : null}
      <section className="approval-grid">
        {!loading && visible.length ? visible.map((approval) => (
          <article className="approval-card" key={approval.id} onClick={() => setSelected(approval)}>
            <Status tone={approvalTone(approval.risk)}>{approval.risk}</Status>
            <span className="eyebrow">{approvalLabel(approval.action)}</span>
            <h2>{approval.id.slice(0, 12)}</h2>
            <p>Exact action digest <code>{approval.actionDigest.slice(0, 16)}…</code></p>
            <div className="approval-card-footer"><small>Expires {new Date(approval.expiresAt).toLocaleString()}</small><button className="primary-button" onClick={(event) => { event.stopPropagation(); setSelected(approval); }}>Review</button></div>
          </article>
        )) : <div className="empty-state"><strong>{loading ? "Loading approval inbox…" : "No approval requests"}</strong><span>{message || "Protected plans, patches, and sandbox permissions will appear here."}</span></div>}
      </section>
      {selected ? <section className="panel section-gap" aria-label="Approval detail"><div className="panel-heading"><div><span className="eyebrow">Approval detail</span><h2>{approvalLabel(selected.action)} · {selected.id.slice(0, 12)}</h2></div><Status tone={selected.state === "approved" ? "success" : selected.state === "pending" ? "warning" : "neutral"}>{selected.state}</Status></div><dl><div><dt>Action digest</dt><dd><code>{selected.actionDigest}</code></dd></div><div><dt>Eligible approvers</dt><dd>{selected.eligibleRoles.join(", ")}</dd></div><div><dt>Requested</dt><dd>{new Date(selected.requestedAt).toLocaleString()}</dd></div></dl>{selected.state === "pending" ? <div className="panel-footer"><button className="secondary-button" onClick={() => void decide(selected, "rejected")}>Reject</button><button className="primary-button" onClick={() => void decide(selected, "approved")}>Approve</button></div> : null}</section> : null}
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

type APIAutomation = {
  id: string;
  name: string;
  description: string;
  kind: "code_review" | "ci_diagnosis" | "issue_repair";
  provider: string;
  enabled: boolean;
  repositoryScope: string[];
  eventFilters: Array<{ event: string; actions: string[]; branches?: string[] }>;
  agentTemplate: string;
  executionBudget: number;
  timeoutSeconds: number;
  concurrencyLimit: number;
  risk: string;
  approvalPolicy: string;
  version: number;
  revision: number;
  updatedAt: string;
};

type AutomationForm = {
  name: string;
  description: string;
  kind: APIAutomation["kind"];
  provider: "github";
  repositoryScope: string;
  event: string;
  actions: string;
  branches: string;
  agentTemplate: string;
  executionBudget: number;
  timeoutSeconds: number;
  concurrencyLimit: number;
  risk: string;
  approvalPolicy: string;
};

type AutomationRun = {
  id: string;
  templateVersion: number;
  triggerKey: string;
  repository: string;
  taskId: string;
  taskStatus: string;
  status: string;
  createdAt: string;
};

const emptyAutomationForm: AutomationForm = {
  name: "", description: "", kind: "code_review", provider: "github", repositoryScope: "",
  event: "pull_request", actions: "opened, synchronize", branches: "main", agentTemplate: "codex-reviewer",
  executionBudget: 100, timeoutSeconds: 300, concurrencyLimit: 2, risk: "high", approvalPolicy: "required",
};

function AutomationsPage({ showToast }: { showToast: (message: string) => void }) {
  const [items, setItems] = useState<APIAutomation[]>([]);
  const [selected, setSelected] = useState<APIAutomation | null>(null);
  const [runs, setRuns] = useState<AutomationRun[]>([]);
  const [form, setForm] = useState<AutomationForm>(emptyAutomationForm);
  const [message, setMessage] = useState("");
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await fetch("/api/v1/automations?limit=100");
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || "automation_unavailable");
      setItems((body.automations ?? []) as APIAutomation[]);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "automation_unavailable");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => void load(), 0);
    return () => window.clearTimeout(timer);
  }, [load]);

  const select = async (item: APIAutomation) => {
    setSelected(item);
    setForm({
      name: item.name, description: item.description, kind: item.kind, provider: "github",
      repositoryScope: item.repositoryScope.join(", "), event: item.eventFilters[0]?.event || "pull_request",
      actions: item.eventFilters[0]?.actions.join(", ") || "opened", branches: item.eventFilters[0]?.branches?.join(", ") || "",
      agentTemplate: item.agentTemplate, executionBudget: item.executionBudget, timeoutSeconds: item.timeoutSeconds,
      concurrencyLimit: item.concurrencyLimit, risk: item.risk, approvalPolicy: item.approvalPolicy,
    });
    try {
      const response = await fetch(`/api/v1/automations/${encodeURIComponent(item.id)}/runs?limit=20`);
      const body = await response.json().catch(() => ({}));
      if (response.ok) setRuns((body.runs ?? []) as AutomationRun[]);
    } catch {
      setRuns([]);
    }
  };

  const updateField = <K extends keyof AutomationForm>(key: K, value: AutomationForm[K]) => setForm((current) => ({ ...current, [key]: value }));
  const adminHeaders = () => {
    const password = window.prompt("Re-authenticate with the local administrator password to change automation configuration.");
    if (!password) return null;
    return { "Content-Type": "application/json", "X-CSRF-Token": csrfCookie(), "X-Reauth-Password": password };
  };
  const body = () => ({
    name: form.name, description: form.description, kind: form.kind, provider: form.provider,
    repositoryScope: form.repositoryScope.split(",").map((value) => value.trim()).filter(Boolean),
    eventFilters: [{ event: form.event, actions: form.actions.split(",").map((value) => value.trim()).filter(Boolean), branches: form.branches.split(",").map((value) => value.trim()).filter(Boolean) }],
    agentTemplate: form.agentTemplate, executionBudget: Number(form.executionBudget), timeoutSeconds: Number(form.timeoutSeconds),
    concurrencyLimit: Number(form.concurrencyLimit), risk: form.risk, approvalPolicy: form.approvalPolicy,
    ...(selected ? { expectedRevision: selected.revision } : {}),
  });
  const save = async () => {
    const headers = adminHeaders();
    if (!headers) return;
    const response = await fetch(selected ? `/api/v1/automations/${encodeURIComponent(selected.id)}` : "/api/v1/automations", {
      method: selected ? "PUT" : "POST", headers, body: JSON.stringify(body()),
    });
    const result = await response.json().catch(() => ({}));
    if (!response.ok) { setMessage(result.error || "automation_save_failed"); return; }
    showToast(selected ? "Automation draft saved" : "Automation draft created");
    setSelected(result.automation);
    await load();
  };
  const publish = async () => {
    if (!selected) return;
    const headers = adminHeaders();
    if (!headers) return;
    const response = await fetch(`/api/v1/automations/${encodeURIComponent(selected.id)}/publish`, { method: "POST", headers, body: JSON.stringify({ expectedRevision: selected.revision }) });
    const result = await response.json().catch(() => ({}));
    if (!response.ok) { setMessage(result.error || "automation_publish_failed"); return; }
    setSelected(result.automation); showToast("Automation version published"); await load();
  };
  const toggle = async () => {
    if (!selected) return;
    const headers = adminHeaders();
    if (!headers) return;
    const action = selected.enabled ? "disable" : "enable";
    const response = await fetch(`/api/v1/automations/${encodeURIComponent(selected.id)}/${action}`, { method: "POST", headers, body: JSON.stringify({ expectedRevision: selected.revision }) });
    const result = await response.json().catch(() => ({}));
    if (!response.ok) { setMessage(result.error || "automation_state_change_failed"); return; }
    setSelected(result.automation); showToast(selected.enabled ? "Automation disabled" : "Automation activated"); await load();
  };

  return (
    <>
      <PageHeader eyebrow="Automation · Administration" title="Automations" description="Versioned templates, scoped triggers, governed budgets, and auditable run history." actions={<><button className="secondary-button" onClick={() => { setSelected(null); setForm(emptyAutomationForm); }}>New template</button><button className="secondary-button" onClick={() => void load()}>Refresh</button></>} />
      {message ? <div className="identity-message" role="status">M9 API: {message}</div> : null}
      <div className="automation-layout">
        <aside className="panel wizard-nav"><div className="panel-heading"><div><span className="eyebrow">Template catalog</span><h2>{loading ? "Loading…" : `${items.length} templates`}</h2></div></div>{items.map((item) => <button key={item.id} className={selected?.id === item.id ? "active" : ""} onClick={() => void select(item)}><i>{item.enabled ? "✓" : "·"}</i><span>{item.name}<small>{item.kind.replaceAll("_", " ")} · v{item.version}</small></span></button>)}{!loading && !items.length ? <div className="empty-state"><strong>No templates</strong><span>Create a draft to start a governed workflow.</span></div> : null}</aside>
        <section className="panel automation-form">
          <div className="panel-heading"><div><span className="eyebrow">{selected ? `Revision ${selected.revision} · version ${selected.version}` : "Draft template"}</span><h2>{selected?.name || "Create automation"}</h2></div>{selected ? <Status tone={selected.enabled ? "success" : "neutral"}>{selected.enabled ? "Active" : "Disabled"}</Status> : null}</div>
          <div className="form-grid">
            <label className="full-field">Name<input value={form.name} onChange={(event) => updateField("name", event.target.value)} placeholder="Payments PR review" /></label>
            <label className="full-field">Description<textarea rows={2} value={form.description} onChange={(event) => updateField("description", event.target.value)} /></label>
            <label>Workflow kind<select value={form.kind} onChange={(event) => { const kind = event.target.value as AutomationForm["kind"]; updateField("kind", kind); updateField("event", kind === "code_review" ? "pull_request" : kind === "ci_diagnosis" ? "workflow_run" : "issues"); updateField("actions", kind === "ci_diagnosis" ? "completed" : "opened"); }}><option value="code_review">Code review</option><option value="ci_diagnosis">CI diagnosis</option><option value="issue_repair">Issue repair</option></select></label>
            <label>Provider<select value={form.provider} disabled><option value="github">GitHub</option></select></label>
            <label className="full-field">Repository scope<input value={form.repositoryScope} onChange={(event) => updateField("repositoryScope", event.target.value)} placeholder="acme/payments, acme/ledger" /></label>
            <label>Event<input value={form.event} onChange={(event) => updateField("event", event.target.value)} /></label>
            <label>Actions<input value={form.actions} onChange={(event) => updateField("actions", event.target.value)} /></label>
            <label className="full-field">Branches<input value={form.branches} onChange={(event) => updateField("branches", event.target.value)} placeholder="main" /></label>
            <label>Agent template<input value={form.agentTemplate} onChange={(event) => updateField("agentTemplate", event.target.value)} /></label>
            <label>Execution budget<input type="number" min={1} value={form.executionBudget} onChange={(event) => updateField("executionBudget", Number(event.target.value))} /></label>
            <label>Timeout seconds<input type="number" min={30} max={3600} value={form.timeoutSeconds} onChange={(event) => updateField("timeoutSeconds", Number(event.target.value))} /></label>
            <label>Concurrency<input type="number" min={1} max={20} value={form.concurrencyLimit} onChange={(event) => updateField("concurrencyLimit", Number(event.target.value))} /></label>
            <label>Risk<select value={form.risk} onChange={(event) => updateField("risk", event.target.value)}><option>low</option><option>medium</option><option>high</option><option>critical</option></select></label>
            <label>Approval policy<select value={form.approvalPolicy} onChange={(event) => updateField("approvalPolicy", event.target.value)}><option value="required">Required</option><option value="risk_based">Risk based</option></select></label>
          </div>
          <div className="panel-footer"><button className="secondary-button" onClick={() => void save()}>Save draft</button>{selected ? <><button className="secondary-button" onClick={() => void publish()}>Publish version</button><button className={selected.enabled ? "danger-button" : "primary-button"} onClick={() => void toggle()}>{selected.enabled ? "Disable" : "Activate"}</button></> : null}</div>
        </section>
      </div>
      {selected ? <section className="panel section-gap"><div className="panel-heading"><div><span className="eyebrow">Run history · exact template version retained</span><h2>{runs.length} recent runs</h2></div><Status tone={selected.enabled ? "success" : "neutral"}>{selected.enabled ? "New triggers accepted" : "No new triggers"}</Status></div>{runs.length ? <div className="table-scroll"><table><thead><tr><th>Run</th><th>Repository</th><th>Config</th><th>Task</th><th>Status</th><th>Created</th></tr></thead><tbody>{runs.map((run) => <tr key={run.id}><td>{run.id.slice(0, 12)}<small>{run.triggerKey}</small></td><td>{run.repository}</td><td>v{run.templateVersion}</td><td>{run.taskId.slice(0, 12)}</td><td><Status tone={taskTone(run.taskStatus)}>{taskStatusLabel(run.taskStatus)}</Status></td><td>{new Date(run.createdAt).toLocaleString()}</td></tr>)}</tbody></table></div> : <div className="empty-state"><strong>No runs yet</strong><span>Webhook delivery creates a run only when provider, event, repository scope, and approval policy match.</span></div>}</section> : null}
    </>
  );
}

function RunsPage({ navigate }: { navigate: (route: string) => void }) {
  const [runs, setRuns] = useState<Array<{ id: string; taskId: string; attempt: number; status: string; correlationId: string; createdAt: string }>>([]);
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState("");

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void (async () => {
        try {
          const response = await fetch("/api/v1/runs?limit=100");
          if (response.status === 401) {
            navigate("/login");
            return;
          }
          const body = await response.json().catch(() => ({}));
          if (!response.ok) throw new Error(body.error || "runs_unavailable");
          setRuns(body.runs ?? []);
        } catch (error) {
          setMessage(error instanceof Error ? error.message : "runs_unavailable");
        } finally {
          setLoading(false);
        }
      })();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [navigate]);

  return (
    <>
      <PageHeader
        eyebrow="Agent Compose"
        title="Runs"
        description="Persistent execution history mapped to tasks, attempts, and run events."
      />
      {message ? <div className="identity-message" role="status">M4 run API unavailable: {message}</div> : null}
      <section className="panel">
        <div className="panel-heading"><div><span className="eyebrow">Durable run history</span><h2>{loading ? "Loading runs…" : `${runs.length} runs`}</h2></div></div>
        {!loading && runs.length ? <div className="table-scroll"><table><thead><tr><th>Run</th><th>Task</th><th>Status</th><th>Attempt</th><th>Created</th></tr></thead><tbody>{runs.map((run) => <tr key={run.id} onClick={() => navigate(`/runs/${encodeURIComponent(run.id)}`)}><td><strong>{run.id.slice(0, 12)}</strong><small>{run.correlationId}</small></td><td>{run.taskId.slice(0, 12)}</td><td><Status tone={taskTone(run.status)}>{taskStatusLabel(run.status)}</Status></td><td>{run.attempt}</td><td>{new Date(run.createdAt).toLocaleString()}</td></tr>)}</tbody></table></div> : <div className="empty-state"><strong>{loading ? "Reading durable run state…" : "No runs found"}</strong><span>{message || "A run appears after a worker claims a task from the PostgreSQL queue."}</span></div>}
      </section>
    </>
  );
}

function RunDetailPage({ id, navigate }: { id: string; navigate: (route: string) => void }) {
  const [detail, setDetail] = useState<{ run: { id: string; taskId: string; status: string; attempt: number; correlationId: string }; events: Array<{ sequence: number; kind: string; message: string; terminal: boolean }> } | null>(null);
  const [message, setMessage] = useState("");
  useEffect(() => {
    const timer = window.setTimeout(() => {
      void (async () => {
        try {
          const response = await fetch(`/api/v1/runs/${encodeURIComponent(id)}`);
          if (response.status === 401) {
            navigate("/login");
            return;
          }
          const body = await response.json().catch(() => ({}));
          if (!response.ok) throw new Error(body.error || "run_unavailable");
          setDetail(body);
        } catch (error) {
          setMessage(error instanceof Error ? error.message : "run_unavailable");
        }
      })();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [id, navigate]);
  if (message) return <><PageHeader eyebrow="Run" title="Run unavailable" description={message} actions={<button className="secondary-button" onClick={() => navigate("/runs")}>Back to runs</button>} /></>;
  if (!detail) return <PageHeader eyebrow="Run" title="Loading run…" description="Reading persisted events and terminal state." />;
  return <><PageHeader eyebrow={`Run · attempt ${detail.run.attempt}`} title={detail.run.id} description={`Task ${detail.run.taskId} · correlation ${detail.run.correlationId}`} actions={<button className="secondary-button" onClick={() => navigate("/runs")}>Back to runs</button>} /><div className="detail-status"><Status tone={taskTone(detail.run.status)}>{taskStatusLabel(detail.run.status)}</Status><span>{detail.events.length} persisted events</span></div><section className="panel"><div className="panel-heading"><div><span className="eyebrow">Run events</span><h2>Ordered stream</h2></div></div><pre className="log-viewer">{detail.events.length ? detail.events.map((event) => `${event.sequence.toString().padStart(6, "0")}  ${event.kind.padEnd(15)} ${event.message}`).join("\n") : "No events persisted for this run."}</pre></section></>;
}

type DiagnosticEvent = {
  sequence: number;
  runId: string;
  kind: string;
  message: string;
  terminal: boolean;
};

function csrfCookie(): string {
  const value = document.cookie.split("; ").find((item) => item.startsWith("repomender_csrf="));
  return value ? decodeURIComponent(value.slice("repomender_csrf=".length)) : "";
}

function ExecutionDiagnosticPage({ navigate }: { navigate: (route: string) => void }) {
  const [form, setForm] = useState({
    projectId: "", agentName: "codex", repository: "", commitSha: "",
    prompt: "Inspect the pinned repository revision and return a concise M3 diagnostic result.",
    timeoutSeconds: 600, driver: "docker",
  });
  const [runId, setRunId] = useState("");
  const [events, setEvents] = useState<DiagnosticEvent[]>([]);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState(false);

  // The diagnostic streams directly from the guarded M3 endpoint. EventSource
  // reconnect offsets are encoded by the backend and no M4 task state is faked.
  const start = async (event: FormEvent) => {
    event.preventDefault();
    setPending(true);
    setMessage("");
    setEvents([]);
    try {
      const response = await fetch("/api/v1/internal/executions", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfCookie() },
        body: JSON.stringify({
          ...form,
          correlationId: window.crypto.randomUUID(),
          timeoutSeconds: Number(form.timeoutSeconds),
          networkEnabled: false,
        }),
      });
      const body = await response.json().catch(() => ({ error: "ac_execution_failed" }));
      if (!response.ok) {
        setMessage(response.status === 404 ? "M3 execution is disabled on this deployment." : `Execution rejected: ${body.error}`);
        return;
      }
      const id = String(body.run.id);
      setRunId(id);
      const source = new EventSource(`/api/v1/internal/executions/${encodeURIComponent(id)}/events`);
      const receive = (streamEvent: MessageEvent) => {
        const item = JSON.parse(streamEvent.data) as DiagnosticEvent;
        setEvents((current) => [...current.slice(-499), item]);
        if (item.terminal) {
          source.close();
          setMessage(`Run ${item.message}.`);
        }
      };
      ["started", "status", "log", "test", "agent_activity", "completed"].forEach((kind) => source.addEventListener(kind, receive as EventListener));
      source.addEventListener("error", (streamEvent) => {
        const payload = streamEvent instanceof MessageEvent ? JSON.parse(streamEvent.data) : { error: "ac_stream_dropped" };
        setMessage(`Stream stopped: ${payload.error}`);
        source.close();
      });
    } catch {
      setMessage("RepoMender API is unavailable.");
    } finally {
      setPending(false);
    }
  };

  const cancel = async () => {
    if (!runId) return;
    const response = await fetch(`/api/v1/internal/executions/${encodeURIComponent(runId)}/cancel`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfCookie() },
      body: JSON.stringify({ reason: "operator requested from diagnostic UI" }),
    });
    setMessage(response.ok ? "Cancellation requested." : "Cancellation could not be requested.");
  };

  return (
    <>
      <PageHeader
        eyebrow="Internal · M3 acceptance"
        title="Agent Compose execution diagnostic"
        description="Start one governed Codex run against an immutable repository commit. This page does not create an M4 task."
        actions={<><button className="secondary-button" onClick={() => navigate("/runs")}>Back to runs</button><button className="danger-button" disabled={!runId} onClick={() => void cancel()}>Cancel run</button></>}
      />
      <div className="diagnosis-grid">
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">Immutable input</span><h2>Diagnostic request</h2></div><Status tone="warning">Network disabled</Status></div>
          <form className="form-grid" onSubmit={start}>
            <label className="full-field">AC project ID<input required value={form.projectId} onChange={(event) => setForm({ ...form, projectId: event.target.value })} /></label>
            <label>Agent name<input required value={form.agentName} onChange={(event) => setForm({ ...form, agentName: event.target.value })} /></label>
            <label>Driver<input required value={form.driver} onChange={(event) => setForm({ ...form, driver: event.target.value })} /></label>
            <label className="full-field">Repository clone URL<input type="url" required value={form.repository} onChange={(event) => setForm({ ...form, repository: event.target.value })} /></label>
            <label className="full-field">Full commit SHA<input required minLength={40} maxLength={64} value={form.commitSha} onChange={(event) => setForm({ ...form, commitSha: event.target.value })} /></label>
            <label>Timeout seconds<input type="number" min={30} max={7200} required value={form.timeoutSeconds} onChange={(event) => setForm({ ...form, timeoutSeconds: Number(event.target.value) })} /></label>
            <label className="full-field">Prompt<textarea rows={5} required value={form.prompt} onChange={(event) => setForm({ ...form, prompt: event.target.value })} /></label>
            <button className="primary-button" disabled={pending}>{pending ? "Starting…" : "Start diagnostic"}</button>
          </form>
          {message ? <div className="identity-message" role="status">{message}</div> : null}
        </section>
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">Ordered SSE</span><h2>{runId ? `Run ${runId.slice(0, 12)}` : "Waiting for a run"}</h2></div><Status tone={events.some((item) => item.terminal) ? "success" : runId ? "info" : "neutral"}>{events.length} events</Status></div>
          <pre className="log-viewer">{events.length ? events.map((item) => `${item.sequence.toString().padStart(6, "0")}  ${item.kind.padEnd(15)} ${item.message}`).join("\n") : "Status, logs, tests, Agent activity, and the terminal event will appear here."}</pre>
        </section>
      </div>
    </>
  );
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
    if (pathname === "/tasks") return <TasksPage navigate={navigate} />;
    if (pathname.startsWith("/tasks/")) return <TaskDetailPage id={decodeURIComponent(pathname.slice("/tasks/".length))} navigate={navigate} />;
    if (pathname === "/reviews") return <TasksPage navigate={navigate} kindFilter="code_review" />;
    if (pathname.startsWith("/reviews/")) return <TaskDetailPage id={decodeURIComponent(pathname.slice("/reviews/".length))} navigate={navigate} backRoute="/reviews" />;
	if (pathname === "/diagnostics") return <TasksPage navigate={navigate} kindFilter="ci_diagnosis" />;
	if (pathname.startsWith("/diagnostics/")) return <TaskDetailPage id={decodeURIComponent(pathname.slice("/diagnostics/".length))} navigate={navigate} backRoute="/diagnostics" />;
    if (pathname === "/repairs") return <ListPage type="Issue Repair" title="Issue repairs" description="Governed issue-to-pull-request workflows with human approval." navigate={navigate} />;
    if (pathname.startsWith("/repairs/")) return <RepairDetail openDialog={setDialog} />;
    if (pathname === "/approvals") return <ApprovalsPage />;
    if (pathname === "/repositories") return <RepositoriesPage navigate={navigate} />;
    if (pathname.startsWith("/repositories/")) return <RepositoryDetail id={decodeURIComponent(pathname.slice("/repositories/".length))} navigate={navigate} />;
    if (pathname === "/automations") return <AutomationsPage showToast={showToast} />;
    if (pathname === "/runs") return <RunsPage navigate={navigate} />;
    if (pathname === "/runs/diagnostic") return <ExecutionDiagnosticPage navigate={navigate} />;
    if (pathname.startsWith("/runs/")) return <RunDetailPage id={decodeURIComponent(pathname.slice("/runs/".length))} navigate={navigate} />;
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
