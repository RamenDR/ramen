# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import secrets
import subprocess

import pytest

from drenv import envfile

# DRIVER can be overriden to allow testing in github when we don't have
# hardware acceleration for VMs.
DRIVER = os.environ.get("DRIVER", "vm")

TEST_ENV = os.path.join("envs", f"{DRIVER}.yaml")


class Env:
    def __init__(self):
        self.prefix = f"test-{secrets.token_hex(16)}-"
        with open(TEST_ENV) as f:
            env = envfile.load(f, name_prefix=self.prefix)
        self.profile = env["profiles"][0]["name"]

    def start(self):
        self._run("start")

    def delete(self):
        self._run("delete")

    def _run(self, cmd):
        subprocess.run(
            ["drenv", cmd, "--verbose", "--name-prefix", self.prefix, TEST_ENV],
            check=True,
        )


@pytest.fixture(scope="session")
def tmpenv():
    env = Env()
    env.start()
    yield env
    env.delete()
