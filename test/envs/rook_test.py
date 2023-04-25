# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
from drenv import envfile

ENV = os.path.join("envs", "rook.yaml")


def test_load():
    with open(ENV) as f:
        envfile.load(f)


def test_load_prefix():
    with open(ENV) as f:
        envfile.load(f, name_prefix="test-")
