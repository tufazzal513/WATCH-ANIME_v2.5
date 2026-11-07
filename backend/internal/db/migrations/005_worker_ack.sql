-- Worker progress is trusted only after the worker confirms its local transaction committed.
ALTER TABLE workers ADD COLUMN last_epoch TEXT NOT NULL DEFAULT '';
