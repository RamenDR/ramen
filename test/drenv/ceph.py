# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
from . import kubectl


def list_osd_blocklist(cluster):
    """
    List osd block list.
    """
    out = tool(cluster, "ceph", "--format=json", "osd", "blocklist", "ls")

    # We get invalid json:
    #
    #   \n[{"addr": "...", "until": "..."}][]
    #
    # Trim the newline at the front and the [] suffix for now.
    # TODO: report ceph bug and find a better way to parse.
    out = out.strip()
    if out.endswith("[]"):
        out = out[:-2]

    return json.loads(out)


def clear_osd_blocklist(cluster):
    """
    Clear ceph osd blocklist.
    """
    tool(cluster, "ceph", "osd", "blocklist", "clear")


def set_config(cluster, who, option, value):
    """
    See https://docs.ceph.com/en/latest/rados/configuration/ceph-conf/#commands
    """
    tool(cluster, "ceph", "config", "set", who, option, value)


def rm_config(cluster, who, option):
    """
    See https://docs.ceph.com/en/latest/rados/configuration/ceph-conf/#commands
    """
    tool(cluster, "ceph", "config", "rm", who, option)


def tool(cluster, *args):
    return kubectl.exec(
        "deploy/rook-ceph-tools",
        "--namespace=rook-ceph",
        "--",
        *args,
        context=cluster,
    )
