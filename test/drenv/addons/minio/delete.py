# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import mcli


def delete(cluster):
    """
    Remove the mcli alias for the cluster.
    """
    print(f"Removing mcli alias {cluster}")
    mcli.remove_alias(cluster)
