# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import envfile


def test_load():
    with open("regional-dr.yaml") as f:
        envfile.load(f)
