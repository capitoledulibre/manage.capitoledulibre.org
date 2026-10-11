-- Pictures already set (the staff avatars of the partner space) were
-- uploaded: mark them so, or the Gravatar fallback would replace them.
UPDATE people SET avatar_source = 'upload' WHERE avatar_file != '' AND avatar_source = '';
