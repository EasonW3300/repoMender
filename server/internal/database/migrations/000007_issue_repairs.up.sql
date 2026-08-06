CREATE TABLE issue_repairs (
    id UUID PRIMARY KEY,
    task_id UUID NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE CASCADE,
    source_key TEXT NOT NULL UNIQUE,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('github_issue', 'm6_diagnosis')),
    repository_id UUID REFERENCES repositories(id) ON DELETE SET NULL,
    repository_name TEXT NOT NULL,
    clone_url TEXT NOT NULL,
    web_url TEXT NOT NULL DEFAULT '',
    installation_id TEXT NOT NULL,
    issue_number INTEGER NOT NULL DEFAULT 0 CHECK (issue_number >= 0),
    issue_title TEXT NOT NULL,
    issue_body TEXT NOT NULL DEFAULT '',
    base_branch TEXT NOT NULL,
    base_sha TEXT NOT NULL CHECK (base_sha ~ '^[0-9a-fA-F]{40}$'),
    state TEXT NOT NULL CHECK (state IN ('planning', 'awaiting_plan_approval', 'plan_approved', 'patching', 'awaiting_patch_approval', 'patch_approved', 'published', 'failed', 'cancelled')),
    plan JSONB NOT NULL DEFAULT '{}'::jsonb,
    plan_digest TEXT NOT NULL DEFAULT '' CHECK (plan_digest = '' OR plan_digest ~ '^[0-9a-fA-F]{64}$'),
    plan_approval_id UUID REFERENCES approval_requests(id) ON DELETE SET NULL,
    patch JSONB NOT NULL DEFAULT '{}'::jsonb,
    patch_digest TEXT NOT NULL DEFAULT '' CHECK (patch_digest = '' OR patch_digest ~ '^[0-9a-fA-F]{64}$'),
    patch_approval_id UUID REFERENCES approval_requests(id) ON DELETE SET NULL,
    branch_name TEXT NOT NULL DEFAULT '',
    draft_pr_number INTEGER NOT NULL DEFAULT 0 CHECK (draft_pr_number >= 0),
    draft_pr_url TEXT NOT NULL DEFAULT '',
    failure_code TEXT NOT NULL DEFAULT '',
    failure_message TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX issue_repairs_state_idx ON issue_repairs (state, updated_at DESC);
CREATE INDEX issue_repairs_repository_idx ON issue_repairs (repository_id, created_at DESC);
