# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import secrets
import subprocess

import pytest

from drenv import envfile


class Env:
    def __init__(self):
        self.prefix = f"test-{secrets.token_hex(8)}-"
        with open("test.yaml") as f:
            env = envfile.load(f, name_prefix=self.prefix)
        self.profile = env["profiles"][0]["name"]

    def start(self):
        self._run("start")

    def delete(self):
        self._run("delete")

    def _run(self, cmd):
        subprocess.run(
            ["drenv", cmd, "--name-prefix", self.prefix, "test.yaml"],
            check=True,
        )


@pytest.fixture(scope="session")
def tmpenv():
    env = Env()
    env.start()
    yield env
    env.delete()
