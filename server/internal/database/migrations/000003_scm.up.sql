CREATE TABLE scm_connections (
    id UUID PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    name TEXT NOT NULL,
    external_account_id TEXT NOT NULL,
    base_url TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'error', 'revoked')),
    credential_ciphertext BYTEA,
    created_by UUID NOT NULL REFERENCES users(id),
    last_synced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, external_account_id)
);

CREATE TABLE repositories (
    id UUID PRIMARY KEY,
    connection_id UUID NOT NULL REFERENCES scm_connections(id) ON DELETE CASCADE,
    provider_repository_id TEXT NOT NULL,
    full_name TEXT NOT NULL,
    clone_url TEXT NOT NULL,
    web_url TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT '',
    visibility TEXT NOT NULL DEFAULT 'private'
        CHECK (visibility IN ('private', 'internal', 'public')),
    archived BOOLEAN NOT NULL DEFAULT false,
    enabled BOOLEAN NOT NULL DEFAULT true,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_synced_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, provider_repository_id)
);

CREATE INDEX repositories_connection_idx ON repositories (connection_id, full_name);

CREATE TABLE scm_oauth_flows (
    state_hash BYTEA PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    verifier TEXT NOT NULL,
    return_to TEXT NOT NULL DEFAULT '/repositories',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX scm_oauth_flows_expiry_idx ON scm_oauth_flows (expires_at);

CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('github', 'gitlab')),
    delivery_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload_sha256 BYTEA NOT NULL,
    normalized_event JSONB NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, delivery_id)
);

CREATE INDEX webhook_deliveries_received_idx ON webhook_deliveries (received_at);
