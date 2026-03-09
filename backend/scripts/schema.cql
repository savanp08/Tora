CREATE TABLE IF NOT EXISTS converse.users (
	id uuid PRIMARY KEY,
	email text,
	password_hash text,
	google_id text,
	username text,
	full_name text,
	avatar_url text,
	created_at timestamp
);

CREATE INDEX IF NOT EXISTS users_email_idx ON converse.users (email);
CREATE INDEX IF NOT EXISTS users_google_id_idx ON converse.users (google_id);
CREATE INDEX IF NOT EXISTS users_username_idx ON converse.users (username);

CREATE TABLE IF NOT EXISTS converse.users_by_username (
	username text PRIMARY KEY,
	user_id uuid,
	created_at timestamp
);

CREATE TABLE IF NOT EXISTS converse.password_reset_tokens (
	email text PRIMARY KEY,
	otp_hash text,
	expires_at timestamp,
	created_at timestamp,
	consumed_at timestamp,
	request_ip text
);

CREATE TABLE IF NOT EXISTS converse.rooms (
	id uuid PRIMARY KEY,
	name text,
	owner_id uuid,
	is_ephemeral boolean,
	expires_at timestamp,
	created_at timestamp
);

CREATE TABLE IF NOT EXISTS converse.user_rooms (
	user_id uuid,
	room_id uuid,
	room_name text,
	role text,
	joined_at timestamp,
	last_accessed timestamp,
	PRIMARY KEY ((user_id), room_id)
) WITH CLUSTERING ORDER BY (room_id ASC);

CREATE TABLE IF NOT EXISTS converse.tasks (
	room_id uuid,
	id uuid,
	title text,
	description text,
	status text,
	assignee_id uuid,
	created_at timestamp,
	updated_at timestamp,
	PRIMARY KEY ((room_id), id)
) WITH CLUSTERING ORDER BY (id ASC);

CREATE INDEX IF NOT EXISTS tasks_assignee_id_idx ON converse.tasks (assignee_id);

ALTER TABLE converse.rooms ADD is_direct boolean;

CREATE TABLE IF NOT EXISTS converse.user_connections (
	user_id uuid,
	target_id uuid,
	status text,
	created_at timestamp,
	PRIMARY KEY (user_id, target_id)
);

CREATE TABLE IF NOT EXISTS converse.personal_items (
	user_id uuid,
	item_id uuid,
	type text,
	content text,
	status text,
	due_at timestamp,
	created_at timestamp,
	PRIMARY KEY ((user_id), item_id)
) WITH CLUSTERING ORDER BY (item_id DESC);
