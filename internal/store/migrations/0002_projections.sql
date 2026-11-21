-- The projections (DESIGN-0004 § Tables). Each is kept by the applies in
-- internal/store and can be rebuilt from events, so there are no foreign
-- keys: an apply must not fail because its events arrived out of order.
--
-- Every row an apply updates carries the source time of the data it
-- holds (the *_at guard columns), and an apply ignores older data.

-- +goose Up
CREATE TABLE installations (
    id            bigint PRIMARY KEY,
    app           text        NOT NULL,
    account_login text        NOT NULL,
    account_type  text        NOT NULL DEFAULT '',
    suspended     boolean     NOT NULL DEFAULT false,
    removed_at    timestamptz,
    updated_at    timestamptz NOT NULL
);

CREATE TABLE repositories (
    id              bigint PRIMARY KEY,
    installation_id bigint      NOT NULL,
    owner           text        NOT NULL,
    slug            text        NOT NULL,
    state           text        NOT NULL DEFAULT 'idle',
    profile         text        NOT NULL DEFAULT '',
    onboarded_at    timestamptz,
    offboarded_at   timestamptz,
    next_due        timestamptz,
    last_run_id     text,
    last_run_at     timestamptz,
    last_outcome    text,
    -- Guards: lifecycle (onboard, offboard, refresh), state, schedule.
    updated_at      timestamptz NOT NULL,
    state_at        timestamptz,
    scheduled_at    timestamptz
);

CREATE INDEX repositories_owner_idx ON repositories (owner, slug);
CREATE INDEX repositories_installation_idx ON repositories (installation_id);

CREATE TABLE runs (
    id                text PRIMARY KEY,
    repo_id           bigint      NOT NULL,
    cause_key         text,
    outcome           text        NOT NULL DEFAULT '',
    repository_result text        NOT NULL DEFAULT '',
    started_at        timestamptz NOT NULL,
    finished_at       timestamptz,
    queued_ms         bigint,
    duration_ms       bigint,
    renovate_version  text        NOT NULL DEFAULT '',
    spend             jsonb       NOT NULL DEFAULT '{}'::jsonb,
    problems          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    updated_at        timestamptz NOT NULL
);

CREATE INDEX runs_repo_started_idx ON runs (repo_id, started_at DESC);
CREATE INDEX runs_started_idx ON runs (started_at DESC);

CREATE TABLE branch_updates (
    repo_id      bigint NOT NULL,
    branch       text   NOT NULL,
    package_file text   NOT NULL,
    package_name text   NOT NULL,
    run_id       text   NOT NULL,
    pr_number    integer,
    manager      text   NOT NULL DEFAULT '',
    datasource   text   NOT NULL DEFAULT '',
    dep_name     text   NOT NULL DEFAULT '',
    update_type  text   NOT NULL DEFAULT '',
    from_version text   NOT NULL DEFAULT '',
    to_version   text   NOT NULL DEFAULT '',
    purl_from    text,
    purl_to      text,
    PRIMARY KEY (repo_id, branch, package_file, package_name)
);

CREATE INDEX branch_updates_purl_from_idx ON branch_updates (purl_from);
CREATE INDEX branch_updates_purl_to_idx ON branch_updates (purl_to);

CREATE TABLE dependencies (
    repo_id         bigint NOT NULL,
    manager         text   NOT NULL,
    package_file    text   NOT NULL,
    package_name    text   NOT NULL,
    run_id          text   NOT NULL,
    dep_name        text   NOT NULL DEFAULT '',
    datasource      text   NOT NULL DEFAULT '',
    current_value   text   NOT NULL DEFAULT '',
    current_version text   NOT NULL DEFAULT '',
    purl            text,
    PRIMARY KEY (repo_id, manager, package_file, package_name)
);

CREATE INDEX dependencies_purl_idx ON dependencies (purl);

CREATE TABLE pull_requests (
    repo_id       bigint      NOT NULL,
    number        integer     NOT NULL,
    branch        text        NOT NULL DEFAULT '',
    state         text        NOT NULL,
    title         text        NOT NULL DEFAULT '',
    labels        text[]      NOT NULL DEFAULT '{}',
    automerge     boolean     NOT NULL DEFAULT false,
    url           text        NOT NULL DEFAULT '',
    opened_at     timestamptz,
    merged_at     timestamptz,
    closed_at     timestamptz,
    gh_updated_at timestamptz NOT NULL,
    PRIMARY KEY (repo_id, number)
);

CREATE INDEX pull_requests_branch_idx ON pull_requests (repo_id, branch);
CREATE INDEX pull_requests_state_opened_idx ON pull_requests (state, opened_at);

CREATE TABLE dashboard_items (
    repo_id       bigint      NOT NULL,
    marker        text        NOT NULL,
    branch        text        NOT NULL,
    section       text        NOT NULL DEFAULT '',
    title         text        NOT NULL DEFAULT '',
    deps          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    first_seen_at timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    ticked_at     timestamptz,
    ticked_by     text,
    PRIMARY KEY (repo_id, marker, branch)
);

CREATE TABLE webhook_deliveries (
    delivery_id     uuid        NOT NULL,
    attempt         integer     NOT NULL DEFAULT 1,
    app             text        NOT NULL DEFAULT '',
    event           text        NOT NULL DEFAULT '',
    action          text        NOT NULL DEFAULT '',
    outcome         text        NOT NULL DEFAULT '',
    github_status   integer,
    missed          boolean     NOT NULL DEFAULT false,
    installation_id bigint,
    repo_id         bigint,
    received_at     timestamptz,
    -- Guard for the delivery audit's results.
    audited_at      timestamptz,
    payload         jsonb,
    PRIMARY KEY (delivery_id, attempt)
);

CREATE INDEX webhook_deliveries_received_idx ON webhook_deliveries (received_at DESC);

CREATE TABLE discovery_passes (
    app         text        NOT NULL,
    started_at  timestamptz NOT NULL,
    finished_at timestamptz,
    seen        integer     NOT NULL DEFAULT 0,
    onboarded   integer     NOT NULL DEFAULT 0,
    failed      integer     NOT NULL DEFAULT 0,
    probe_cost  integer     NOT NULL DEFAULT 0,
    PRIMARY KEY (app, started_at)
);

-- dashboards guards dashboard_items: the time of the dashboard body the
-- repository's items were last replaced from.
CREATE TABLE dashboards (
    repo_id    bigint PRIMARY KEY,
    updated_at timestamptz NOT NULL
);

-- store_meta is one row: the schema version every role can read, so a
-- role refuses to start below the version it needs (RequireVersion).
CREATE TABLE store_meta (
    only_row       boolean PRIMARY KEY DEFAULT true CHECK (only_row),
    schema_version integer NOT NULL
);

INSERT INTO store_meta (schema_version) VALUES (2);

-- +goose Down
DROP TABLE store_meta;
DROP TABLE dashboards;
DROP TABLE discovery_passes;
DROP TABLE webhook_deliveries;
DROP TABLE dashboard_items;
DROP TABLE pull_requests;
DROP TABLE dependencies;
DROP TABLE branch_updates;
DROP TABLE runs;
DROP TABLE repositories;
DROP TABLE installations;
