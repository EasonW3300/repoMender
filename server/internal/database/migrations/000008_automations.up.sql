CREATE TABLE automation_templates (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (kind IN ('code_review', 'ci_diagnosis', 'issue_repair')),
    provider TEXT NOT NULL CHECK (provider IN ('github')),
    enabled BOOLEAN NOT NULL DEFAULT false,
    repository_scope TEXT[] NOT NULL CHECK (cardinality(repository_scope) BETWEEN 1 AND 100),
    event_filters JSONB NOT NULL,
    agent_template TEXT NOT NULL,
    execution_budget INTEGER NOT NULL CHECK (execution_budget BETWEEN 1 AND 100000),
    timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds BETWEEN 30 AND 3600),
    concurrency_limit INTEGER NOT NULL CHECK (concurrency_limit BETWEEN 1 AND 20),
    risk TEXT NOT NULL CHECK (risk IN ('low', 'medium', 'high', 'critical')),
    approval_policy TEXT NOT NULL CHECK (approval_policy IN ('required', 'risk_based')),
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    published_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    UNIQUE (name)
);

CREATE INDEX automation_templates_enabled_idx ON automation_templates (enabled, updated_at DESC);

CREATE TABLE automation_template_versions (
    template_id UUID NOT NULL REFERENCES automation_templates(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    enabled BOOLEAN NOT NULL,
    config JSONB NOT NULL,
    changed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (template_id, version)
);

CREATE TABLE automation_runs (
    id UUID PRIMARY KEY,
    template_id UUID NOT NULL REFERENCES automation_templates(id) ON DELETE CASCADE,
    template_version INTEGER NOT NULL CHECK (template_version > 0),
    trigger_key TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (provider IN ('github')),
    event TEXT NOT NULL,
    action TEXT NOT NULL,
    repository TEXT NOT NULL,
    task_id UUID NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('scheduled', 'completed', 'failed', 'cancelled')),
    source_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (template_id, trigger_key),
    FOREIGN KEY (template_id, template_version)
        REFERENCES automation_template_versions(template_id, version)
);

CREATE INDEX automation_runs_template_idx ON automation_runs (template_id, created_at DESC);
CREATE INDEX automation_runs_status_idx ON automation_runs (status, created_at DESC);
