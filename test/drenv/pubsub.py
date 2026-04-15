# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Simple publish-subscribe for cross-scheduler notifications.

Subscribers register callbacks for keys. When a key is posted, all
registered callbacks are called with the key. Thread-safe for
concurrent posts from multiple schedulers.

Example usage:

    from queue import Queue
    from drenv.pubsub import PubSub

    ps = PubSub()

    # Subscriber creates a queue and subscribes.
    q = Queue()
    ps.subscribe("hub/ocm-hub", q.put)

    # Publisher posts when addon completes.
    ps.post("hub/ocm-hub")

    # Subscriber receives the key.
    key = q.get()  # "hub/ocm-hub"
"""

import logging
import threading

log = logging.getLogger("pubsub")


class PubSub:
    """
    Publish-subscribe for task completion notifications.
    """

    def __init__(self):
        self._subscribers = {}
        self._lock = threading.Lock()

    def subscribe(self, key, cb):
        """
        Register a callback for a key.

        The callback is called with the key when it is posted.
        Multiple callbacks can be registered for the same key.
        """
        with self._lock:
            self._subscribers.setdefault(key, []).append(cb)
        log.debug("Subscribed to %s", key)

    def post(self, key):
        """
        Post a key to all subscribers.

        Calls all registered callbacks for the key. Callbacks are
        called in registration order. If no subscribers exist for
        the key, this is a no-op.
        """
        with self._lock:
            callbacks = list(self._subscribers.get(key, []))
        if not callbacks:
            log.debug("No subscribers for %s, dropping", key)
            return
        for cb in callbacks:
            cb(key)
        log.debug("Posted %s to %d subscribers", key, len(callbacks))
