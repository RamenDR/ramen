#!/usr/bin/env python3
# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Test script for drenv tool installation.
Run from the test/ directory or project root.
"""

import logging
import sys

from drenv.tools import tools

# Add test directory to path so we can import drenv
sys.path.insert(0, ".")

if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(message)s")

    print("Testing kustomize installation...")
    print()

    try:
        tools.setup()
        print("\n✓ Test successful!")
        sys.exit(0)
    except Exception as e:
        print(f"\n✗ Test failed: {e}")
        import traceback

        traceback.print_exc()
        sys.exit(1)
