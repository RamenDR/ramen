# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import threading

from contextlib import contextmanager

_lock = threading.Lock()
_started = False


class Started(Exception):
    """
    Raised when an operation is cancelled due to shutdown.
    """


def start():
    global _started
    with _lock:
        logging.debug("[main] Starting shutdown")
        _started = True


def started():
    with _lock:
        return _started


@contextmanager
def guard():
    with _lock:
        if _started:
            raise Started
        yield
