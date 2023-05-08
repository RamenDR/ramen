# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0


def register(commands):
    parser = commands.add_parser(
        "deploy",
        help="Deploy ramen on the hub and managed clusters",
    )
    parser.set_defaults(func=run)


def run(args):
    pass
