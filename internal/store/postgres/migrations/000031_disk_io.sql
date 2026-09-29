-- Per-instance disk limits of a plan: {disk_read_mbps, disk_write_mbps,
-- disk_read_iops, disk_write_iops}; a missing or 0 value is unlimited.
ALTER TABLE plans ADD COLUMN IF NOT EXISTS disk_io jsonb NOT NULL DEFAULT '{}'::jsonb;

-- The node's measured disk speed and the runtimes that cannot enforce disk
-- limits, as its agent last reported them; plan settings suggest limits
-- from it.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS disk_perf jsonb;
