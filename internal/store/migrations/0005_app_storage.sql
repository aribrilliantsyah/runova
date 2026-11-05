-- App storage: the saved folders of each app (JSON list of {path, dir}), the
-- disk size allocated to it from the owner's disk quota, and the result of the
-- last size scan of its workspace. disk_mb 0 marks an app created before disk
-- quotas: its usage is measured and shown but not limited until a size is set.
ALTER TABLE users ADD COLUMN quota_disk_mb INTEGER NOT NULL DEFAULT 1024;
ALTER TABLE apps ADD COLUMN folders TEXT NOT NULL DEFAULT '[]';
ALTER TABLE apps ADD COLUMN disk_mb INTEGER NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN disk_used_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN disk_scanned_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN disk_level TEXT NOT NULL DEFAULT '';
