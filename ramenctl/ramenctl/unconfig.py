# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0


def register(commands):
    parser = commands.add_parser(
        "unconfig",
        help="Unconfigure ramen hub operator",
    )
    parser.set_defaults(func=run)


def run(args):
    pass
