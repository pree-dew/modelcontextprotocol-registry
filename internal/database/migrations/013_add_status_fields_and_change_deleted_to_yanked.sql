-- Add status management fields and change 'deleted' status to 'yanked'
-- This migration adds support for deprecation and yanking features

BEGIN;

-- Add new columns for status management
ALTER TABLE servers ADD COLUMN status_changed_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE servers ADD COLUMN status_message TEXT;
ALTER TABLE servers ADD COLUMN alternative_url TEXT;
ALTER TABLE servers ADD COLUMN new_name VARCHAR(255);

-- Initialize status_changed_at with published_at for existing records
UPDATE servers SET status_changed_at = published_at WHERE status_changed_at IS NULL;

-- Make status_changed_at NOT NULL now that all records have values
ALTER TABLE servers ALTER COLUMN status_changed_at SET NOT NULL;

-- Update constraint to include 'yanked' status and remove 'deleted'
ALTER TABLE servers DROP CONSTRAINT check_status_valid;
ALTER TABLE servers ADD CONSTRAINT check_status_valid
CHECK (status IN ('active', 'deprecated', 'yanked'));

-- Change existing 'deleted' status to 'yanked'
UPDATE servers SET status = 'yanked' WHERE status = 'deleted';

-- Add index for new_name lookups
CREATE INDEX idx_servers_new_name ON servers(new_name);

-- Validation: new_name must be a valid server name format if set
ALTER TABLE servers ADD CONSTRAINT check_new_name_format
    CHECK (new_name IS NULL OR new_name ~ '^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$');

-- Constraint: status_changed_at must be >= published_at
ALTER TABLE servers ADD CONSTRAINT check_status_changed_at_after_published
    CHECK (status_changed_at >= published_at);

COMMIT;
