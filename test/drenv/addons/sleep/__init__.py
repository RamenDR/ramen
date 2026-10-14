# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import commands


def start(duration):
    """
    Simulate an addon that takes time to complete.
    """
    print(f"Sleep addon waiting {duration} seconds")
    commands.run("sleep", str(duration))
    print("Sleep addon finished normally")
