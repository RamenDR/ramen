# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import time


def on_error(
    func,
    args=(),
    kwargs={},
    count=5,
    error=Exception,
    duration=1.0,
    factor=2.0,
    cap=30.0,
    log=None,
):
    """
    Retry function call on specified error up to count times.

    Arguments:
        func(callable): function to call.
        args(sequence): optional function arguments.
        kwargs(mapping): optional function keyword arguments.
        count(int): number of retries on errors.
        error(exception): retry the only on specified exception. Any other
            exception will fail imediately.
        duration(float): initial duration to wait between attempts.
        factor(float): multiple duration on every attempt.
        cap(float): maximum duration between attempts.
        log(logging.Logger): optional logger.

    Returns:
        The function result on success.
    Raises:
        Exception raised by func on the last failed attempt.
    """
    duration = min(duration, cap)
    for i in range(1, count + 1):
        try:
            return func(*args, **kwargs)
        except error as e:
            if i == count:
                raise

            if log:
                log.debug("Attempt %d failed: %s", i, e)
            time.sleep(duration)
            if duration < cap:
                duration = min(duration * factor, cap)
