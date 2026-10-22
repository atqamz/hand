ALTER TABLE device ADD COLUMN phone TEXT NOT NULL DEFAULT '' CHECK (phone = '' OR length(phone) = 16);
