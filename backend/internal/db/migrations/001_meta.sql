-- Key/value store for small internal values (replication epoch, counters, flags).
-- It is included in every backup; it is NOT replicated to workers.
CREATE TABLE meta (
    k TEXT PRIMARY KEY,
    v TEXT NOT NULL
) WITHOUT ROWID;
