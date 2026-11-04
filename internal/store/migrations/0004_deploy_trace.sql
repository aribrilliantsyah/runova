-- Deployment trace: the steps of the rollout, the diagnosed problem of a
-- failed deploy or crash, and the last output of a container kept after the
-- container itself is removed.
ALTER TABLE deployments ADD COLUMN steps TEXT NOT NULL DEFAULT '[]';
ALTER TABLE deployments ADD COLUMN problem TEXT NOT NULL DEFAULT '';
ALTER TABLE deployments ADD COLUMN container_log TEXT NOT NULL DEFAULT '';
