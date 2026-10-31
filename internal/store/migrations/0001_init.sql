CREATE TABLE users(
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL CHECK(role IN('admin','user')),
	quota_apps INTEGER NOT NULL,
	quota_memory_mb INTEGER NOT NULL,
	quota_cpu_milli INTEGER NOT NULL,
	quota_databases INTEGER NOT NULL,
	quota_pg_connections INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	net_index INTEGER NULL UNIQUE,
	pg_password TEXT NOT NULL DEFAULT ''
);
CREATE TABLE sessions(
	token_hash TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE settings(
	id INTEGER PRIMARY KEY CHECK(id = 1),
	value TEXT NOT NULL
);
CREATE TABLE login_failures(
	username TEXT NOT NULL,
	ip TEXT NOT NULL,
	at INTEGER NOT NULL
);
CREATE INDEX login_failures_key ON login_failures(username, ip, at);
CREATE TABLE activity(
	id INTEGER PRIMARY KEY,
	at INTEGER NOT NULL,
	actor_id INTEGER NULL REFERENCES users ON DELETE SET NULL,
	actor_name TEXT NOT NULL,
	action TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT ''
);
CREATE INDEX activity_at ON activity(at);
CREATE TABLE uploads(
	id TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	dir TEXT NOT NULL,
	artifact_path TEXT NOT NULL,
	report TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX uploads_created ON uploads(created_at);
CREATE TABLE databases(
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	name TEXT NOT NULL UNIQUE,
	created_at INTEGER NOT NULL
);
CREATE INDEX databases_user ON databases(user_id);
CREATE TABLE apps(
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	java_version INTEGER NULL,
	jvm_opts TEXT NOT NULL DEFAULT '',
	internal_port INTEGER NOT NULL,
	ip_port INTEGER NOT NULL UNIQUE,
	ip_access INTEGER NOT NULL DEFAULT 0,
	memory_mb INTEGER NOT NULL,
	cpu_milli INTEGER NOT NULL,
	status TEXT NOT NULL,
	current_deployment_id INTEGER NULL,
	deploy_mode TEXT NOT NULL DEFAULT 'stopstart',
	created_at INTEGER NOT NULL,
	database_id INTEGER NULL REFERENCES databases ON DELETE RESTRICT,
	UNIQUE(user_id, name)
);
CREATE TABLE app_env(
	app_id INTEGER NOT NULL REFERENCES apps ON DELETE CASCADE,
	name TEXT NOT NULL,
	value TEXT NOT NULL,
	secret INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(app_id, name)
);
CREATE TABLE deployments(
	id INTEGER PRIMARY KEY,
	app_id INTEGER NOT NULL REFERENCES apps ON DELETE CASCADE,
	number INTEGER NOT NULL,
	artifact_path TEXT NOT NULL,
	file_name TEXT NOT NULL,
	sha256 TEXT NOT NULL,
	report TEXT NOT NULL,
	env_snapshot TEXT NOT NULL DEFAULT '',
	java_version INTEGER NULL,
	jvm_opts TEXT NOT NULL DEFAULT '',
	internal_port INTEGER NOT NULL DEFAULT 0,
	host_port INTEGER NOT NULL DEFAULT 0,
	container_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	failure_log TEXT NOT NULL DEFAULT '',
	created_by INTEGER NULL REFERENCES users ON DELETE SET NULL,
	created_at INTEGER NOT NULL,
	finished_at INTEGER NOT NULL DEFAULT 0,
	UNIQUE(app_id, number)
);
CREATE TABLE notifications(
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	at INTEGER NOT NULL,
	level TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	app_id INTEGER NULL,
	read INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX notifications_user ON notifications(user_id, at);
CREATE TABLE domains(
	id INTEGER PRIMARY KEY,
	app_id INTEGER NOT NULL REFERENCES apps ON DELETE CASCADE,
	host TEXT NOT NULL UNIQUE,
	status TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	checked_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX domains_app ON domains(app_id);
