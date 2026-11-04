ALTER TABLE attempt ADD COLUMN access TEXT NOT NULL DEFAULT 'open' CHECK (access IN ('open', 'readonly'));
