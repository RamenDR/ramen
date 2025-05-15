# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import os
import shutil

import drenv
from drenv import commands

SSH_DIR = drenv.config_dir("ssh")
PRIVATE_KEY = os.path.join(SSH_DIR, "id_rsa")
PUBLIC_KEY = PRIVATE_KEY + ".pub"


def setup():
    """
    Called during drenv steup.
    """
    if os.path.exists(PRIVATE_KEY):
        logging.debug("[ssh] Using existing private key '%s'", PRIVATE_KEY)
        return

    logging.debug("[ssh] Generating private key '%s'", PRIVATE_KEY)
    os.makedirs(SSH_DIR, mode=0o700, exist_ok=True)
    commands.run("ssh-keygen", "-f", PRIVATE_KEY, "-N", "", "-t", "rsa", "-q")


def cleanup():
    """
    Called during drenv cleanup.
    """
    logging.debug("[ssh] Removing '%s'", SSH_DIR)
    shutil.rmtree(SSH_DIR, ignore_errors=True)
