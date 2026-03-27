-- +goose Up
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'
  CHECK (status IN ('active', 'suspended'));

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS status;
