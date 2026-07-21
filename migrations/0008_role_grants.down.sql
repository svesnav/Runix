DELETE FROM grants WHERE subject_type = 'role';
ALTER TABLE grants DROP CONSTRAINT grants_subject_type_check;
ALTER TABLE grants ADD CONSTRAINT grants_subject_type_check
    CHECK (subject_type IN ('user', 'group'));
