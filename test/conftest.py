# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os

import pytest

from drenv import envfile

# DRIVER can be overriden to allow testing in github when we don't have
# hardware acceleration for VMs.
DRIVER = os.environ.get("DRIVER", "vm")

TEST_ENV = os.path.join("envs", f"{DRIVER}.yaml")


class Env:
    def __init__(self):
        self.prefix = "drenv-test-"
        with open(TEST_ENV) as f:
            env = envfile.load(f, name_prefix=self.prefix)
        self.profile = env["profiles"][0]["name"]


@pytest.fixture(scope="session")
def tmpenv():
    return Env()
