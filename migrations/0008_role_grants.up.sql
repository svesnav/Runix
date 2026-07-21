-- Roles become grant subjects alongside users and user groups, so a scoped
-- permission can be given to everyone holding a role ("operators may
-- restart runtimes on this server").
ALTER TABLE grants DROP CONSTRAINT grants_subject_type_check;
ALTER TABLE grants ADD CONSTRAINT grants_subject_type_check
    CHECK (subject_type IN ('user', 'group', 'role'));
