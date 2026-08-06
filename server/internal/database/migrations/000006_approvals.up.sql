CREATE TABLE approval_requests (
    id UUID PRIMARY KEY,
    action TEXT NOT NULL CHECK (action IN ('repair_plan', 'publish_patch', 'sandbox_network')),
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'approved', 'rejected', 'expired', 'cancelled', 'consumed')),
    risk TEXT NOT NULL CHECK (risk IN ('low', 'medium', 'high', 'critical')),
    action_digest TEXT NOT NULL CHECK (action_digest ~ '^[0-9a-fA-F]{64}$'),
    requester_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    eligible_roles TEXT[] NOT NULL CHECK (cardinality(eligible_roles) > 0),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    decision_idempotency_key TEXT,
    decision_actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    decision_reason TEXT NOT NULL DEFAULT '',
    decided_at TIMESTAMPTZ,
    consumption_idempotency_key TEXT,
    consumed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    consumed_at TIMESTAMPTZ,
    cancelled_by UUID REFERENCES users(id) ON DELETE SET NULL,
    cancel_reason TEXT NOT NULL DEFAULT '',
    cancelled_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (expires_at > requested_at),
    UNIQUE (id, decision_idempotency_key),
    UNIQUE (id, consumption_idempotency_key)
);

CREATE INDEX approval_requests_inbox_idx
    ON approval_requests (state, risk DESC, expires_at, requested_at DESC);
CREATE INDEX approval_requests_requester_idx
    ON approval_requests (requester_id, requested_at DESC);
