# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0


def merge(a, b):
    """
    Merge b into a recursively.
    """
    if type(a) is dict:
        assert type(b) is dict
        res = dict(a)
        for k, v in b.items():
            if k in res:
                res[k] = merge(res[k], v)
            else:
                res[k] = v
        return res
    else:
        return a if b is None else b
