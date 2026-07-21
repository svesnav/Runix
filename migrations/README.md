# Database migrations

PostgreSQL schema changes are applied exclusively through versioned
migrations in this directory, using the
[golang-migrate](https://github.com/golang-migrate/migrate) file convention:

```
NNNN_short_description.up.sql
NNNN_short_description.down.sql
```

Rules:

- Never edit an applied migration; add a new one.
- Every `up` must have a working `down`.
- Migrations must be safe to run on a live system (no long exclusive locks
  on large tables without a comment explaining why it is acceptable).
- The first migration lands together with the first persistent module
  (users/auth).
