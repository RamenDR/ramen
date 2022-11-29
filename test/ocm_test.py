# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import envfile


def test_load():
    with open("ocm.yaml") as f:
        envfile.load(f)


def test_load_prefix():
    with open("ocm.yaml") as f:
        envfile.load(f, name_prefix="test-")
