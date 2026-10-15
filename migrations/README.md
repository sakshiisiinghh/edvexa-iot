# Migrations

Applied in lexical filename order by `make migrate`, tracked in
`schema_migrations`, and never rolled back — to undo something, write a new
forward migration.

## Every migration body must be idempotent

This is a requirement, not a style preference. `make migrate` checks
`schema_migrations` before applying a file, but that check runs *outside* the
advisory lock, so two simultaneous runs can both decide to apply the same file.
What makes that safe is the lock serialising the apply, the body sharing one
transaction with its tracking insert, and the body being re-runnable.

So use:

- `CREATE TABLE IF NOT EXISTS`
- `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`
- `CREATE INDEX IF NOT EXISTS`
- a `DO` block guard for constraints, which have no `IF NOT EXISTS`:

```sql
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'my_table_some_key'
    ) THEN
        ALTER TABLE my_table ADD CONSTRAINT my_table_some_key UNIQUE (col);
    END IF;
END $$;
```

A non-idempotent body will not corrupt anything — the second attempt errors and
rolls back — but it produces a confusing failure. Mechanically enforcing this
is what a migration library would buy, and is the concrete reason to adopt one
if the schema outgrows this approach.
