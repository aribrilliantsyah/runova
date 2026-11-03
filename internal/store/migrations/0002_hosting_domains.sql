CREATE TABLE hosting_domains(
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	enabled INTEGER NOT NULL DEFAULT 1,
	reserved TEXT NOT NULL DEFAULT '',
	dns_status TEXT NOT NULL DEFAULT 'unchecked',
	dns_detail TEXT NOT NULL DEFAULT '',
	dns_checked_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
ALTER TABLE domains ADD COLUMN hosting_domain_id INTEGER NULL REFERENCES hosting_domains ON DELETE RESTRICT;
ALTER TABLE domains ADD COLUMN label TEXT NOT NULL DEFAULT '';
CREATE INDEX domains_hosting ON domains(hosting_domain_id);
