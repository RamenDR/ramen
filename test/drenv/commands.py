# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import selectors

OUT = "out"
ERR = "err"

_Selector = getattr(selectors, "PollSelector", selectors.SelectSelector)


def stream(proc, bufsize=32 << 10):
    """
    Stream data from process stdout and stderr.

    proc is a subprocess.Popen instance created with stdout=subprocess.PIPE and
    stderr=subprocess.PIPE. If only one stream is used don't use this, stream
    directly from the single pipe.

    Yields either (OUT, data) or (ERR, data) read from proc stdout and stderr.
    Returns when both streams are closed.
    """
    with _Selector() as sel:
        for f, src in (proc.stdout, OUT), (proc.stderr, ERR):
            if f and not f.closed:
                sel.register(f, selectors.EVENT_READ, src)

        while sel.get_map():
            for key, event in sel.select():
                data = os.read(key.fd, bufsize)
                if not data:
                    sel.unregister(key.fileobj)
                    key.fileobj.close()
                    continue

                yield key.data, data
