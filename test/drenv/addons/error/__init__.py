# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import sys
import time


def start(duration):
    """
    Simulate an addon that fails after a delay.
    """
    print(f"Error addon will fail in {duration} seconds")
    time.sleep(duration)
    sys.exit("Error addon failed")
