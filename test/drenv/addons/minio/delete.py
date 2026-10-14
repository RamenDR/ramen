# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import mc


def delete(cluster):
    """
    Remove the mc alias for the cluster.
    """
    print(f"Removing mc alias {cluster}")
    mc.remove_alias(cluster)
