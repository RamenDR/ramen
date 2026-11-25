# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import threading
import time
import timeit

import pytest

from drenv import scheduler

DELAY = 0.1

log = logging.getLogger("test")


def Key(name):
    """Create a Key with "test" context for single-context tests."""
    return scheduler.Key("test", name)


# --- No dependencies ---


def test_single_task():
    tasks = [scheduler.Task(Key("a"), data="a")]
    s = scheduler.Scheduler(tasks)
    s.run(quick)
    assert s.events == [(scheduler.START, Key("a")), (scheduler.COMPLETE, Key("a"))]


def test_independent_tasks():
    """Tasks with no dependencies all run."""
    tasks = [
        scheduler.Task(Key("a"), data="a"),
        scheduler.Task(Key("b"), data="b"),
        scheduler.Task(Key("c"), data="c"),
    ]
    s = scheduler.Scheduler(tasks)
    s.run(quick)

    assert_all_ran(s.events, {Key("a"), Key("b"), Key("c")})


def test_independent_tasks_run_in_parallel():
    """Independent tasks run concurrently, verified by barrier."""
    barrier = threading.Barrier(3, timeout=5)

    def sync(task):
        log.debug("sync %s", task.key)
        barrier.wait()

    tasks = [scheduler.Task(Key(n), data=n) for n in "abc"]
    s = scheduler.Scheduler(tasks)
    s.run(sync)
    assert_all_ran(s.events, {Key("a"), Key("b"), Key("c")})


def test_empty():
    """No tasks raises ValueError."""
    with pytest.raises(ValueError, match="tasks must not be empty"):
        scheduler.Scheduler([])


# --- Linear dependency chain ---


def test_linear_chain():
    """A -> B -> C runs in strict order."""
    tasks = [
        scheduler.Task(Key("a"), data="a"),
        scheduler.Task(Key("b"), requires=[Key("a")], data="b"),
        scheduler.Task(Key("c"), requires=[Key("b")], data="c"),
    ]
    # Use max_workers=3 to allow all tasks to run in parallel.
    # This ensures the ordering is enforced by dependencies, not
    # by a concurrency limit.
    s = scheduler.Scheduler(tasks, max_workers=3)
    s.run(quick)
    assert s.events == [
        (scheduler.START, Key("a")),
        (scheduler.COMPLETE, Key("a")),
        (scheduler.START, Key("b")),
        (scheduler.COMPLETE, Key("b")),
        (scheduler.START, Key("c")),
        (scheduler.COMPLETE, Key("c")),
    ]


# --- Fan-out (one dependency, multiple dependents) ---


def test_fan_out():
    """Multiple tasks become ready when their shared dependency completes."""
    barrier = threading.Barrier(3, timeout=5)

    def sync_children(task):
        log.debug("sync_children %s", task.key)
        if task.key != Key("base"):
            barrier.wait()

    tasks = [
        scheduler.Task(Key("base"), data="base"),
        scheduler.Task(Key("child1"), requires=[Key("base")], data="child1"),
        scheduler.Task(Key("child2"), requires=[Key("base")], data="child2"),
        scheduler.Task(Key("child3"), requires=[Key("base")], data="child3"),
    ]
    s = scheduler.Scheduler(tasks, max_workers=4)
    s.run(sync_children)

    # Base must complete before any child starts.
    assert s.events[:2] == [
        (scheduler.START, Key("base")),
        (scheduler.COMPLETE, Key("base")),
    ]

    # All children started and completed.
    assert_all_ran(s.events, {Key("base"), Key("child1"), Key("child2"), Key("child3")})


# --- Fan-in (multiple dependencies) ---


def test_fan_in():
    """Task waits for all its dependencies."""
    tasks = [
        scheduler.Task(Key("a"), data="a"),
        scheduler.Task(Key("b"), data="b"),
        scheduler.Task(Key("c"), requires=[Key("a"), Key("b")], data="c"),
    ]
    s = scheduler.Scheduler(tasks, max_workers=3)
    s.run(quick)

    # c must run last, after both a and b complete.
    assert s.events[-2:] == [
        (scheduler.START, Key("c")),
        (scheduler.COMPLETE, Key("c")),
    ]
    assert_all_ran(s.events, {Key("a"), Key("b"), Key("c")})


# --- Mixed dependencies and independent tasks ---


def test_rook_with_independent_tasks():
    """
    Rook chain plus independent tasks that run in parallel.
    Models a realistic dr-cluster profile.
    """
    tasks = [
        scheduler.Task(Key("rook-operator"), data="rook-operator"),
        scheduler.Task(
            Key("rook-cluster"), requires=[Key("rook-operator")], data="rook-cluster"
        ),
        scheduler.Task(
            Key("rook-pool"), requires=[Key("rook-cluster")], data="rook-pool"
        ),
        scheduler.Task(
            Key("rook-cephfs"), requires=[Key("rook-cluster")], data="rook-cephfs"
        ),
        scheduler.Task(Key("ocm-cluster"), data="ocm-cluster"),
        scheduler.Task(Key("olm"), data="olm"),
        scheduler.Task(Key("minio"), data="minio"),
    ]
    # Use max_workers=4 so all initially ready tasks (rook-operator,
    # ocm-cluster, olm, minio) are submitted in the first batch.
    s = scheduler.Scheduler(tasks, max_workers=4)
    s.run(quick)

    assert_all_ran(s.events, {t.key for t in tasks})

    # Independent tasks started before rook-operator completed.
    assert started_before_completed(s.events, Key("ocm-cluster"), Key("rook-operator"))
    assert started_before_completed(s.events, Key("olm"), Key("rook-operator"))
    assert started_before_completed(s.events, Key("minio"), Key("rook-operator"))


# --- max_workers ---


def test_max_workers_limits_concurrency():
    """max_workers limits how many tasks run at once."""
    sem = threading.Semaphore(2)

    def track(task):
        if not sem.acquire(blocking=False):
            raise AssertionError("too many concurrent tasks")
        try:
            time.sleep(0.01)  # Force overlap between tasks.
        finally:
            sem.release()

    tasks = [scheduler.Task(Key(n), data=n) for n in "abcdef"]
    s = scheduler.Scheduler(tasks, max_workers=2)
    s.run(track)


def test_max_workers_one():
    """max_workers=1 runs tasks sequentially in insertion order."""
    tasks = [
        scheduler.Task(Key("a"), data="a"),
        scheduler.Task(Key("b"), data="b"),
        scheduler.Task(Key("c"), data="c"),
    ]
    s = scheduler.Scheduler(tasks, max_workers=1)
    s.run(quick)
    assert s.events == [
        (scheduler.START, Key("a")),
        (scheduler.COMPLETE, Key("a")),
        (scheduler.START, Key("b")),
        (scheduler.COMPLETE, Key("b")),
        (scheduler.START, Key("c")),
        (scheduler.COMPLETE, Key("c")),
    ]


# --- Dependency validation ---


def test_unknown_dependency():
    """Unknown dependency raises with task and dependency names."""
    tasks = [
        scheduler.Task(
            Key("rook-pool"), requires=[Key("rook-opreator")], data="rook-pool"
        )
    ]
    with pytest.raises(scheduler.DependencyError, match="rook-pool.*rook-opreator"):
        scheduler.Scheduler(tasks)


# --- Cycle detection ---


def test_cycle_two_tasks():
    """Two tasks depending on each other."""
    tasks = [
        scheduler.Task(Key("a"), requires=[Key("b")], data="a"),
        scheduler.Task(Key("b"), requires=[Key("a")], data="b"),
    ]
    with pytest.raises(scheduler.CycleError, match="Dependency cycle"):
        scheduler.Scheduler(tasks)


def test_cycle_three_tasks():
    """Three tasks forming a cycle."""
    tasks = [
        scheduler.Task(Key("a"), requires=[Key("c")], data="a"),
        scheduler.Task(Key("b"), requires=[Key("a")], data="b"),
        scheduler.Task(Key("c"), requires=[Key("b")], data="c"),
    ]
    with pytest.raises(scheduler.CycleError, match="Dependency cycle"):
        scheduler.Scheduler(tasks)


def test_self_dependency():
    """Task depending on itself."""
    tasks = [scheduler.Task(Key("a"), requires=[Key("a")], data="a")]
    with pytest.raises(scheduler.CycleError, match="Dependency cycle"):
        scheduler.Scheduler(tasks)


def test_cycle_cross_context():
    """Cycle detection works with keys from different contexts."""
    tasks = [
        scheduler.Task(
            scheduler.Key("hub", "a"), requires=[scheduler.Key("dr1", "b")], data="a"
        ),
        scheduler.Task(
            scheduler.Key("dr1", "b"), requires=[scheduler.Key("hub", "a")], data="b"
        ),
    ]
    with pytest.raises(scheduler.CycleError, match="Dependency cycle"):
        scheduler.Scheduler(tasks)


# --- Error handling ---


def test_dependent_task_not_run_after_failure():
    """If a dependency fails, its dependents are not started."""
    tasks = [
        scheduler.Task(Key("a"), data="a"),
        scheduler.Task(Key("b"), requires=[Key("a")], data="b"),
    ]
    started = []

    def tracking_run(task):
        started.append(task.data)
        if task.data == "a":
            raise RuntimeError("task a failed")

    s = scheduler.Scheduler(tasks)
    with pytest.raises(RuntimeError):
        s.run(tracking_run)

    assert "b" not in started
    assert (scheduler.FAILED, Key("a")) in s.events
    assert (scheduler.START, Key("b")) not in s.events


# --- Stress test ---


def test_stress():
    """200 tasks, chains of 5 every 10 tasks, 4 workers."""
    task_count = 200

    tasks = []
    for t in range(task_count):
        key = scheduler.Key("c0", f"t{t}")
        # Chain tasks within each group of 5 (at offsets 0-4, 10-14, ...).
        pos = t % 10
        requires = [scheduler.Key("c0", f"t{t - 1}")] if 0 < pos < 5 else []
        tasks.append(scheduler.Task(key, requires=requires, data=None))

    def run():
        s = scheduler.Scheduler(tasks, max_workers=8)
        s.run(quick)

    t = timeit.timeit(run, number=10)
    print(f"scheduled {task_count} tasks in {t / 10 * 1000:.3f} ms")


# --- Cross-context dependencies ---


def test_cross_context_deps():
    """Tasks with keys from different contexts can depend on each other."""
    hub_ocm = scheduler.Key("hub", "ocm-hub")
    dr1_rook = scheduler.Key("dr1", "rook-operator")
    dr1_ocm = scheduler.Key("dr1", "ocm-cluster")

    tasks = [
        scheduler.Task(hub_ocm, data="ocm-hub"),
        scheduler.Task(dr1_rook, data="rook-operator"),
        scheduler.Task(dr1_ocm, requires=[dr1_rook, hub_ocm], data="ocm-cluster"),
    ]
    s = scheduler.Scheduler(tasks, max_workers=2)
    s.run(quick)

    assert_all_ran(s.events, {hub_ocm, dr1_rook, dr1_ocm})

    # ocm-cluster must start after both deps complete.
    assert s.events[-2:] == [
        (scheduler.START, dr1_ocm),
        (scheduler.COMPLETE, dr1_ocm),
    ]


# --- String representation ---


def test_key_str():
    assert str(scheduler.Key("dr1", "rook-operator")) == "dr1/rook-operator"


def test_task_str_no_deps():
    t = scheduler.Task(scheduler.Key("hub", "ready"))
    assert str(t) == "hub/ready"


def test_task_str_with_deps():
    t = scheduler.Task(
        scheduler.Key("dr1", "ocm-cluster"),
        requires=[scheduler.Key("dr1", "ready"), scheduler.Key("hub", "ocm-hub")],
    )
    assert str(t) == "dr1/ocm-cluster -> [dr1/ready, hub/ocm-hub]"


# --- Data passthrough ---


def test_task_passed_to_run_func():
    """Scheduler passes task to run_func with accessible data."""
    received = []

    tasks = [
        scheduler.Task(Key("a"), data="addon-a"),
        scheduler.Task(Key("b"), data="addon-b"),
    ]
    s = scheduler.Scheduler(tasks, max_workers=1)
    s.run(lambda task: received.append(task.data))

    assert received == ["addon-a", "addon-b"]


# --- Helpers ---


def quick(task):
    """Run without delay - for tests that only check scheduling order."""
    log.debug("quick %s", task.key)


def all_started(events):
    """Return set of task keys that were started."""
    return {n for e, n in events if e == scheduler.START}


def all_completed(events):
    """Return set of task keys that completed successfully."""
    return {n for e, n in events if e == scheduler.COMPLETE}


def assert_all_ran(events, expected):
    """Assert all expected tasks were started and completed."""
    assert all_started(events) == expected
    assert all_completed(events) == expected


def started_before_completed(events, task_key, dependency_key):
    """
    Return True if task_key was started before dependency_key
    completed.
    """
    start_idx = events.index((scheduler.START, task_key))
    complete_idx = events.index((scheduler.COMPLETE, dependency_key))
    return start_idx < complete_idx


def started_after_completed(events, task_key, dependency_key):
    """
    Return True if task_key was started after dependency_key
    completed.
    """
    start_idx = events.index((scheduler.START, task_key))
    complete_idx = events.index((scheduler.COMPLETE, dependency_key))
    return start_idx > complete_idx
