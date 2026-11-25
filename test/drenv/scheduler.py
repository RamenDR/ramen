# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Dependency-based task scheduler.

Schedules tasks for execution based on their dependencies. Tasks with no unmet
dependencies run in parallel, up to a configurable concurrency limit
(max_workers). When a task completes, newly unblocked tasks are scheduled
automatically.

The scheduler is generic and does not know about addons, hooks, or clusters. It
works with Task objects that carry a Key (context + name), a list of dependency
keys, and opaque data passed to the run function.  The key's context is used for
log message prefixes.

To minimize total run time, put the longest task chains first in the task list.
For example, if rook-operator (40s) -> rook-cluster (80s) is the longest chain,
list it before short independent tasks like minio (15s).  This ensures the
critical chain starts as early as possible, and short tasks fill the remaining
worker slots.

Example usage:

    tasks = [
        Task(key=Key("hub", "ocm-hub"), data=ocm_hub_addon),
        Task(key=Key("hub", "submariner"),
             requires=[Key("hub", "ocm-hub")], data=sub_addon),
        Task(key=Key("dr1", "rook-operator"), data=rook_op_addon),
        Task(key=Key("dr1", "rook-cluster"),
             requires=[Key("dr1", "rook-operator")], data=rook_addon),
        Task(key=Key("dr1", "ocm-cluster"),
             requires=[Key("hub", "ocm-hub")], data=ocm_addon),
    ]

    scheduler = Scheduler(tasks, max_workers=4)
    scheduler.run(lambda task: task.data.start(task.key.context))
"""

import concurrent.futures
import logging
from collections import namedtuple

log = logging.getLogger("scheduler")

START = "start"
COMPLETE = "complete"
FAILED = "failed"


class CycleError(Exception):
    """Raised when the dependency graph contains a cycle."""


class DependencyError(Exception):
    """Raised when a task requires an unknown dependency."""


class Key(namedtuple("Key", ["context", "name"])):
    """
    Unique task identifier with a context for log messages.

    Attributes:
        context: Grouping context for log messages (e.g. cluster name).
        name: Task name within the context (e.g. addon name).
    """

    def __str__(self):
        return f"{self.context}/{self.name}"


class Task(namedtuple("Task", ["key", "requires", "data"])):
    """
    A unit of work for the scheduler.

    Attributes:
        key: Unique task identifier (a Key instance). The scheduler
             uses key.context for log message prefixes.
        requires: Frozenset of keys this task depends on.
        data: Opaque data passed to run_func. The scheduler does not
              inspect this.
    """

    def __new__(cls, key, requires=(), data=None):
        return super().__new__(cls, key, frozenset(requires), data)

    def __str__(self):
        if self.requires:
            deps = ", ".join(str(k) for k in sorted(self.requires, key=str))
            return f"{self.key} -> [{deps}]"
        return str(self.key)


class Scheduler:
    """
    Schedule tasks for execution based on dependencies.

    Tasks move through three stages: pending, running, completed.
    Maintains an event log accessible via the events property.
    """

    def __init__(self, tasks, max_workers=None):
        """
        Initialize the scheduler.

        Args:
            tasks: List of Task objects. Task order controls priority
                   when multiple tasks are ready.
            max_workers: Maximum number of tasks to run concurrently.
                         None means unlimited (bounded only by
                         dependencies).

        Raises:
            ValueError: If tasks is empty.
            DependencyError: If a task requires an unknown dependency.
            CycleError: If the dependency graph contains a cycle.
        """
        # Pending tasks, ordered by insertion. Supports O(1) removal
        # when a task is submitted.
        self._pending = self._validate(tasks)

        # Tasks submitted to the executor, mapped by future.
        self._running = {}

        # Keys of tasks that finished successfully.
        self._completed = set()

        self._max_workers = max_workers

        # Event log for inspecting execution order.
        self._events = []

    @property
    def events(self):
        """
        Return a copy of the event log.

        Each event is a (event, key) tuple where event is START,
        COMPLETE, or FAILED.
        """
        return list(self._events)

    def run(self, run_func):
        """
        Run all tasks respecting dependencies.

        Calls run_func(task) for each task. Tasks whose dependencies
        are satisfied run in parallel up to max_workers.

        Args:
            run_func: Callable taking a Task. Called once per task.

        Raises:
            Exception: If any task's run_func raises, the scheduler
                       stops and propagates the exception.
        """
        executor = concurrent.futures.ThreadPoolExecutor(
            max_workers=self._max_workers,
        )

        try:
            while self._pending or self._running:
                self._submit_ready_tasks(executor, run_func)
                if self._running:
                    self._wait_for_completion()
        finally:
            executor.shutdown(wait=False, cancel_futures=True)

    def _submit_ready_tasks(self, executor, run_func):
        """
        Submit ready tasks to the executor up to available slots.

        Submitted tasks are moved from pending to running.
        """
        while True:
            if self._max_workers and len(self._running) >= self._max_workers:
                break
            task = self._next_ready_task()
            if task is None:
                break
            del self._pending[task.key]
            self._log_event(START, task.key)
            future = executor.submit(run_func, task)
            self._running[future] = task.key

    def _wait_for_completion(self):
        """
        Wait for at least one running task to complete.

        Completed tasks are moved from running to completed.
        """
        completed, _ = concurrent.futures.wait(
            self._running,
            return_when=concurrent.futures.FIRST_COMPLETED,
        )

        for future in completed:
            key = self._running.pop(future)
            try:
                future.result()
            except Exception:
                self._log_event(FAILED, key)
                raise
            self._completed.add(key)
            self._log_event(COMPLETE, key)

    def _next_ready_task(self):
        """
        Return the next pending task that has all deps satisfied.

        Returns the first ready task in insertion order, preserving
        the caller's priority order.

        Returns None when no task is ready.
        """
        for task in self._pending.values():
            if task.requires <= self._completed:
                return task
        return None

    def _log_event(self, event, key):
        self._events.append((event, key))
        if event == START:
            log.debug("[%s] Starting %s", key.context, key.name)
        elif event == COMPLETE:
            log.debug("[%s] %s completed", key.context, key.name)
        elif event == FAILED:
            log.debug("[%s] %s failed", key.context, key.name)
        else:
            assert False, f"unexpected event: {event!r}"

    def _validate(self, tasks):
        """
        Validate tasks and build the pending map.

        Checks for empty input, unknown dependencies, and cycles.

        Returns:
            Ordered dict mapping task keys to tasks.
        """
        if not tasks:
            raise ValueError("tasks must not be empty")

        pending = {t.key: t for t in tasks}

        for key, task in pending.items():
            for dep in task.requires:
                if dep not in pending:
                    raise DependencyError(
                        f"Task {key!r} requires unknown dependency {dep!r}"
                    )

        # Detect cycles using DFS. A node visited while still on the
        # current path (in "visiting") indicates a cycle.
        visiting = set()
        visited = set()

        def visit(key, path):
            visiting.add(key)
            for dep in pending[key].requires:
                if dep in visiting:
                    cycle = path[path.index(dep) :] + [dep]
                    raise CycleError(
                        f"Dependency cycle: {' -> '.join(str(k) for k in cycle)}"
                    )
                if dep not in visited:
                    visit(dep, path + [dep])
            visiting.discard(key)
            visited.add(key)

        for key in pending:
            if key not in visited:
                visit(key, [key])

        return pending
