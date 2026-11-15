-- OAuth (Firebase) users have no password and are verified by the provider.
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

ALTER TABLE users
    ADD COLUMN firebase_uid      TEXT,
    ADD COLUMN auth_provider     TEXT NOT NULL DEFAULT 'local',
    ADD COLUMN email_verified_at TIMESTAMPTZ;

ALTER TABLE users ADD CONSTRAINT users_auth_provider_valid CHECK (auth_provider IN ('local', 'google', 'both'));

-- Every existing account was confirmed with an email code (or seeded).
UPDATE users SET email_verified_at = created_at WHERE email_verified_at IS NULL;

CREATE UNIQUE INDEX users_firebase_uid_key ON users (firebase_uid) WHERE firebase_uid IS NOT NULL;
