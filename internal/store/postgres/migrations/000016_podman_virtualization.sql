ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_virtualization_check;
ALTER TABLE plans ADD CONSTRAINT plans_virtualization_check CHECK (virtualization IN ('lxc', 'kvm', 'podman'));
