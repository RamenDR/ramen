# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import threading
from queue import Queue

from drenv.pubsub import PubSub

log = logging.getLogger("test")


def test_subscribe_and_post():
    """Subscriber receives posted key."""
    ps = PubSub()
    received = []
    ps.subscribe("a", received.append)
    ps.post("a")
    assert received == ["a"]


def test_multiple_subscribers():
    """Multiple subscribers receive the same key."""
    ps = PubSub()
    r1 = []
    r2 = []
    ps.subscribe("a", r1.append)
    ps.subscribe("a", r2.append)
    ps.post("a")
    assert r1 == ["a"]
    assert r2 == ["a"]


def test_post_without_subscribers():
    """Posting a key with no subscribers is a no-op."""
    ps = PubSub()
    ps.post("a")  # Should not raise.


def test_subscribe_different_keys():
    """Subscribers only receive their subscribed keys."""
    ps = PubSub()
    ra = []
    rb = []
    ps.subscribe("a", ra.append)
    ps.subscribe("b", rb.append)
    ps.post("a")
    assert ra == ["a"]
    assert rb == []


def test_post_multiple_times():
    """Callback is called each time the key is posted."""
    ps = PubSub()
    received = []
    ps.subscribe("a", received.append)
    ps.post("a")
    ps.post("a")
    assert received == ["a", "a"]


def test_concurrent_usage():
    """Posts from multiple threads are safe."""
    ps = PubSub()

    # s1 wants "a" from all publishers.
    s1 = Queue()
    ps.subscribe("t1/a", s1.put)
    ps.subscribe("t2/a", s1.put)
    ps.subscribe("t3/a", s1.put)

    # s2 wants all events from t1.
    s2 = Queue()
    ps.subscribe("t1/a", s2.put)
    ps.subscribe("t1/b", s2.put)
    ps.subscribe("t1/c", s2.put)

    # Subscribers run in threads, waiting on their queues.
    s1_received = set()
    s2_received = set()

    def receive(name, q, expected_count, received):
        for _ in range(expected_count):
            key = q.get(timeout=5)
            log.debug("%s received %s", name, key)
            received.add(key)

    def publish(thread_id):
        ps.post(f"{thread_id}/a")
        ps.post(f"{thread_id}/b")
        ps.post(f"{thread_id}/c")

    # All subscriptions are registered, start all threads.
    threads = [
        threading.Thread(target=receive, args=("s1", s1, 3, s1_received)),
        threading.Thread(target=receive, args=("s2", s2, 3, s2_received)),
        threading.Thread(target=publish, args=("t1",)),
        threading.Thread(target=publish, args=("t2",)),
        threading.Thread(target=publish, args=("t3",)),
    ]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    # Each subscriber gets only subscribed keys.
    # Unsubscribed keys (t2/b, t2/c, t3/b, t3/c) are dropped.
    assert s1_received == {"t1/a", "t2/a", "t3/a"}
    assert s2_received == {"t1/a", "t1/b", "t1/c"}
