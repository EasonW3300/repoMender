export type Tone = "critical" | "high" | "warning" | "info" | "success" | "neutral";

export type TaskRecord = {
  id: string;
  type: "Code Review" | "CI Diagnosis" | "Issue Repair";
  title: string;
  repository: string;
  status: string;
  tone: Tone;
  agent: string;
  duration: string;
  updated: string;
  route: string;
};

export const tasks: TaskRecord[] = [
  {
    id: "PR #1842",
    type: "Code Review",
    title: "Prevent duplicate payment capture",
    repository: "payments-api",
    status: "Critical finding",
    tone: "critical",
    agent: "Senior Go Reviewer",
    duration: "4m 18s",
    updated: "2 minutes ago",
    route: "/reviews/1842",
  },
  {
    id: "CI #9812",
    type: "CI Diagnosis",
    title: "PostgreSQL 16 integration tests",
    repository: "payments-api",
    status: "Awaiting approval",
    tone: "info",
    agent: "CI Diagnostician",
    duration: "6m 42s",
    updated: "8 minutes ago",
    route: "/diagnostics/pg16",
  },
  {
    id: "Issue #731",
    type: "Issue Repair",
    title: "Retry webhook delivery with exponential backoff",
    repository: "payments-api",
    status: "Plan approval",
    tone: "warning",
    agent: "Senior Go Repair",
    duration: "2m 09s",
    updated: "12 minutes ago",
    route: "/repairs/731",
  },
  {
    id: "PR #1836",
    type: "Code Review",
    title: "Enforce merchant-level idempotency scope",
    repository: "payments-api",
    status: "Passed",
    tone: "success",
    agent: "Security Reviewer",
    duration: "3m 51s",
    updated: "42 minutes ago",
    route: "/reviews/1842",
  },
  {
    id: "CI #9798",
    type: "CI Diagnosis",
    title: "Webhook worker race detector",
    repository: "event-gateway",
    status: "Patch verified",
    tone: "success",
    agent: "CI Diagnostician",
    duration: "8m 16s",
    updated: "1 hour ago",
    route: "/diagnostics/pg16",
  },
  {
    id: "Issue #728",
    type: "Issue Repair",
    title: "Expose settlement retry metrics",
    repository: "ledger-service",
    status: "Running",
    tone: "info",
    agent: "Senior Go Repair",
    duration: "11m 04s",
    updated: "1 hour ago",
    route: "/repairs/731",
  },
];

export const repositories = [
  { name: "payments-api", team: "Payments Platform", language: "Go", health: 79, findings: 3, ci: "At risk", tone: "high" as Tone },
  { name: "event-gateway", team: "Core Infrastructure", language: "TypeScript", health: 92, findings: 1, ci: "Healthy", tone: "success" as Tone },
  { name: "ledger-service", team: "Payments Platform", language: "Go", health: 86, findings: 5, ci: "Healthy", tone: "success" as Tone },
  { name: "merchant-console", team: "Merchant Experience", language: "TypeScript", health: 74, findings: 8, ci: "Degraded", tone: "warning" as Tone },
];

export const approvals = [
  {
    title: "Create repair pull request",
    context: "Issue #731 · payments-api",
    summary: "Agent will create a Draft PR containing 3 files and 126 changed lines.",
    risk: "High risk",
    tone: "critical" as Tone,
    meta: "$1.84 · Senior Go Repair",
  },
  {
    title: "Approve repair plan",
    context: "Issue #731 · webhook delivery",
    summary: "Four stages add bounded exponential backoff and retry classification.",
    risk: "Medium",
    tone: "warning" as Tone,
    meta: "Affects background delivery",
  },
  {
    title: "Allow sandbox network access",
    context: "CI Diagnosis · sbx_8f2",
    summary: "Allow packages.acme.dev for 15 minutes to reproduce dependency installation.",
    risk: "Medium",
    tone: "warning" as Tone,
    meta: "Time-bound permission",
  },
  {
    title: "Apply suggested patch",
    context: "PR #1842 · payments-api",
    summary: "Add a transaction boundary and idempotency constraint.",
    risk: "Standard",
    tone: "info" as Tone,
    meta: "1 critical finding",
  },
];

export const navigation = [
  {
    label: "Overview",
    items: [
      { label: "Dashboard", route: "/", glyph: "D" },
      { label: "My work", route: "/tasks", glyph: "M" },
    ],
  },
  {
    label: "Engineering",
    items: [
      { label: "Code reviews", route: "/reviews", glyph: "R" },
      { label: "CI diagnostics", route: "/diagnostics", glyph: "C" },
      { label: "Issue repairs", route: "/repairs", glyph: "I" },
      { label: "Approvals", route: "/approvals", glyph: "A", count: 5 },
    ],
  },
  {
    label: "Assets",
    items: [
      { label: "Repositories", route: "/repositories", glyph: "B" },
      { label: "Automations", route: "/automations", glyph: "W" },
    ],
  },
  {
    label: "Platform",
    items: [
      { label: "Runs", route: "/runs", glyph: "▶" },
      { label: "Settings", route: "/settings", glyph: "S" },
    ],
  },
];

