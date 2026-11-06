-- CPU is shared: every app may use all server cores with an equal weight, so
-- neither users nor apps carry a CPU quota or allocation any more. Memory and
-- disk stay divided. Neither column is indexed or constrained, so SQLite's
-- ALTER TABLE ... DROP COLUMN removes them in place.
ALTER TABLE users DROP COLUMN quota_cpu_milli;
ALTER TABLE apps DROP COLUMN cpu_milli;
