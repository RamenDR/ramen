# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import argparse
import logging
import sys
import time

from . import (
    deploy,
    undeploy,
    config,
    unconfig,
)

LOG_FORMAT = "%(asctime)s %(levelname)-7s [%(name)s] %(message)s"

COMMANDS = [
    deploy,
    undeploy,
    config,
    unconfig,
]

log = logging.getLogger("ramendev")


def main():
    parser = argparse.ArgumentParser(
        prog="ramendev",
        description="Control ramen clusters",
    )

    # Parsing commands set args.command to the command name.
    # Sub parsers set args.func to the command function.
    parser.set_defaults(verbose=False, command=None, func=None)

    commands = parser.add_subparsers(
        title="commands",
        metavar="COMMAND",
        dest="command",
    )
    for module in COMMANDS:
        module.register(commands)

    args = parser.parse_args()
    if args.command is None:
        parser.print_help()
        sys.exit(1)

    logging.basicConfig(
        level=logging.DEBUG if args.verbose else logging.INFO,
        format=LOG_FORMAT,
    )

    log.debug("Using arguments %s", args)

    log.info("Starting %s", args.command)
    start = time.monotonic()

    args.func(args)

    log.info("Finished %s in %.2f seconds", args.command, time.monotonic() - start)


if __name__ == "__main__":
    main()
