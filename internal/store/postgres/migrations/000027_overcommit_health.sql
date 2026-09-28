-- Overselling is allowed within published ratios. A node's sellable
-- capacity is what its agent reports times its own ratio, which cannot
-- exceed the platform maximum, and never more than a staff cap.
ALTER TABLE system_settings
    ADD COLUMN max_overcommit_cpu numeric(4,2) NOT NULL DEFAULT 4 CHECK (max_overcommit_cpu BETWEEN 1 AND 20),
    ADD COLUMN max_overcommit_ram numeric(4,2) NOT NULL DEFAULT 1.5 CHECK (max_overcommit_ram BETWEEN 1 AND 20),
    ADD COLUMN max_overcommit_disk numeric(4,2) NOT NULL DEFAULT 2 CHECK (max_overcommit_disk BETWEEN 1 AND 20),
    ADD COLUMN max_overcommit_traffic numeric(4,2) NOT NULL DEFAULT 3 CHECK (max_overcommit_traffic BETWEEN 1 AND 20);

ALTER TABLE nodes
    ADD COLUMN reported_vcpu integer,
    ADD COLUMN reported_ram_mb bigint,
    ADD COLUMN reported_disk_gb bigint,
    ADD COLUMN overcommit_cpu numeric(4,2) NOT NULL DEFAULT 1 CHECK (overcommit_cpu >= 1),
    ADD COLUMN overcommit_ram numeric(4,2) NOT NULL DEFAULT 1 CHECK (overcommit_ram >= 1),
    ADD COLUMN overcommit_disk numeric(4,2) NOT NULL DEFAULT 1 CHECK (overcommit_disk >= 1),
    ADD COLUMN overcommit_traffic numeric(4,2) NOT NULL DEFAULT 1 CHECK (overcommit_traffic >= 1),
    -- machine_id groups nodes whose agents run on one machine, so its
    -- hardware is not sold twice.
    ADD COLUMN machine_id text,
    -- health is the agent's last load sample; each *_since marks when a
    -- threshold was first crossed and clears on recovery.
    ADD COLUMN health jsonb,
    ADD COLUMN health_mem_since timestamptz,
    ADD COLUMN health_disk_since timestamptz,
    ADD COLUMN health_load_since timestamptz,
    -- A health hold stops new sales until the node recovers.
    ADD COLUMN health_hold_reason text,
    ADD COLUMN health_hold_since timestamptz;

UPDATE nodes SET reported_vcpu = capacity_vcpu, reported_ram_mb = capacity_ram_mb, reported_disk_gb = capacity_disk_gb;

CREATE INDEX nodes_machine_id_idx ON nodes (machine_id) WHERE machine_id IS NOT NULL;
