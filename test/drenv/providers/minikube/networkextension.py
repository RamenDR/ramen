# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
List macOS network extensions (e.g. VPN, content filters) via systemextensionsctl.

Public API: list_extensions(). parse_output() exists for testability only.
"""

import collections
import logging

import drenv

CATEGORY = "com.apple.system_extension.network_extension"

NetworkExtension = collections.namedtuple(
    "NetworkExtension",
    ["enabled", "active", "team_id", "bundle_id", "name", "state"],
    defaults=(False, False, "", "", "", ""),
)


def list_extensions():
    """
    Return list of NetworkExtension from this Mac.
    """
    out = drenv.commands.run("systemextensionsctl", "list", CATEGORY)
    return parse_output(out)


def parse_output(out):
    """
    Parse systemextensionsctl output and return list of NetworkExtension. For testing only.
    """
    result = []
    for line in out.splitlines():
        fields = line.split("\t")
        if len(fields) != 6:
            continue

        enabled_char, active_char, team_id, bundle_id, name, state = fields
        enabled = _parse_bool(enabled_char)
        active = _parse_bool(active_char)
        if enabled is None or active is None:
            logging.warning(
                "[networkextension] skipping line with invalid enabled/active: %r", line
            )
            continue

        state = state.strip().removeprefix("[").removesuffix("]")
        result.append(
            NetworkExtension(
                enabled=enabled,
                active=active,
                team_id=team_id,
                bundle_id=bundle_id,
                name=name,
                state=state,
            )
        )

    return result


def _parse_bool(char):
    return {"*": True, " ": False}.get(char)
