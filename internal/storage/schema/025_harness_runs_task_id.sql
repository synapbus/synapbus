-- 025: Link harness runs to tasks.
--
-- When a harness run fires in the context of a task (i.e. an agent
-- working on a claimed task), the reactor sets ExecRequest.TaskID and
-- the harness Observer writes it here so the Web UI and the HTML
-- report can correlate runs → tasks.

ALTER TABLE harness_runs ADD COLUMN task_id INTEGER;

CREATE INDEX idx_harness_runs_task ON harness_runs(task_id);
