ALTER TABLE plans
    ADD COLUMN assign_nat boolean NOT NULL DEFAULT true,
    ADD COLUMN port_mapping_count integer NOT NULL DEFAULT 0 CHECK (port_mapping_count >= 0),
    ADD COLUMN assign_ipv4 boolean NOT NULL DEFAULT false,
    ADD COLUMN ipv4_count integer NOT NULL DEFAULT 1 CHECK (ipv4_count > 0),
    ADD COLUMN assign_ipv6 boolean NOT NULL DEFAULT true,
    ADD COLUMN ipv6_count integer NOT NULL DEFAULT 1 CHECK (ipv6_count > 0),
    ADD CONSTRAINT plans_network_policy_check CHECK (assign_nat OR assign_ipv4 OR assign_ipv6);
