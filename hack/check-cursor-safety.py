#!/usr/bin/env python3
# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Check Cursor IDE and CLI configuration for unsafe settings.
"""

import argparse
import json
import os
import sys
import textwrap

import yaml

CURSOR_DIR = os.path.expanduser("~/.cursor")

OK = "ok"
INFO = "info"
WARNING = "warning"
CRITICAL = "critical"

_STATUSES = {
    OK: "ok ✅",
    CRITICAL: "critical ❌",
    WARNING: "warning ⚠️",
    INFO: "info ℹ️",
}

# Check severity order: most severe first, OK excluded (it's the default).
_SEVERITIES = (CRITICAL, WARNING, INFO)

# Block scalar content in the YAML output is indented 6 columns:
# files: / - checks: / - risk: |- / ^^^^^^
YAML_INDENT = 6

# Each check defines a JSON key path (dot-separated), the expected safe value,
# and the risk/suggestion for unsafe values.
#
# Key path supports:
#   "key"           - top-level key
#   "key.subkey"    - nested key
#
# Value checks:
#   "ok"       - values considered safe (list, shown as ok)
#   "critical" - values with critical risk (dict: value -> {risk, suggestion})
#   "warning"  - values with warning risk (dict: value -> {risk, suggestion})
#   "info"     - values with informational risk (dict: value -> {risk, suggestion})
#
# Special types:
#   "type": "non_empty_list" - checks if the value is a non-empty list

CHECKS = [
    {
        "file": os.path.join(CURSOR_DIR, "cli-config.json"),
        "label": "CLI config",
        "properties": [
            {
                "key": "approvalMode",
                "ok": ["allowlist", "manual"],
                "critical": {
                    "unrestricted": {
                        "risk": (
                            "The agent can run any shell command, write files, and "
                            "call MCP tools without asking for your approval. A prompt "
                            "injection or agent mistake can delete files, push code, "
                            "exfiltrate data."
                        ),
                        "suggestion": (
                            'Set "approvalMode": "allowlist". With an empty '
                            "allowlist, every tool call requires your approval."
                        ),
                    },
                },
            },
            {
                "key": "sandbox.mode",
                "ok": ["enabled"],
                "warning": {
                    "disabled": {
                        "risk": (
                            "Shell commands run with your full user permissions. "
                            "The agent can read/write files anywhere on your "
                            "system, access the network, and run destructive "
                            "commands outside the workspace."
                        ),
                        "suggestion": (
                            'Set "sandbox": {"mode": "enabled"}. The sandbox '
                            "restricts file writes to the workspace and limits "
                            "network access."
                        ),
                    },
                },
            },
            {
                "key": "permissions.allow",
                "type": "non_empty_list",
                "warning": {
                    "non_empty": {
                        "risk": (
                            "Auto-approved tool calls run without prompting. "
                            "Broad patterns like Shell(*) or Write(*) bypass "
                            "all approval."
                        ),
                        "suggestion": (
                            "Review and remove unneeded entries from "
                            "permissions.allow."
                        ),
                    },
                },
            },
            {
                "key": "permissions.deny",
                "type": "non_empty_list",
                "info": {
                    "empty": {
                        "risk": (
                            "No tool calls are explicitly blocked. If the "
                            "agent is approved to run a command, nothing "
                            "prevents dangerous operations like "
                            "'rm -rf' or 'git push --force'."
                        ),
                        "suggestion": (
                            "Add dangerous patterns to permissions.deny, for "
                            "example:\n"
                            '"deny": ["Shell(rm -rf *)", '
                            '"Shell(git push --force *)"]'
                        ),
                    },
                },
            },
            {
                "key": "attribution.attributeCommitsToAgent",
                "ok": [False],
                "warning": {
                    True: {
                        "risk": (
                            "Git commits are attributed to the agent instead of "
                            "you. This can obscure authorship in git history and "
                            "may violate your project's contribution guidelines."
                        ),
                        "suggestion": (
                            "Set attributeCommitsToAgent to false."
                        ),
                    },
                },
            },
            {
                "key": "attribution.attributePRsToAgent",
                "ok": [False],
                "warning": {
                    True: {
                        "risk": (
                            "Pull requests are created under the agent's "
                            "identity instead of yours. Reviewers may not know "
                            "who is responsible for the changes."
                        ),
                        "suggestion": "Set attributePRsToAgent to false.",
                    },
                },
            },
        ],
    },
    {
        "file": os.path.join(CURSOR_DIR, "permissions.json"),
        "label": "Global permissions",
        "optional": True,
        "properties": [
            {
                "key": "approvalMode",
                "ok": ["manual", "allowlist"],
                "critical": {
                    "unrestricted": {
                        "risk": (
                            "Overrides approval mode to unrestricted. All tool "
                            "calls bypass approval regardless of other settings."
                        ),
                        "suggestion": (
                            'Set "approvalMode": "manual", or remove the file '
                            "to use the CLI/IDE default."
                        ),
                    },
                },
            },
            {
                "key": "terminalAllowlist",
                "type": "non_empty_list",
                "warning": {
                    "non_empty": {
                        "risk": (
                            "Terminal commands matching these patterns are "
                            "auto-approved without prompting."
                        ),
                        "suggestion": "Review and remove unneeded entries.",
                    },
                },
            },
            {
                "key": "mcpAllowlist",
                "type": "non_empty_list",
                "warning": {
                    "non_empty": {
                        "risk": (
                            "MCP tool calls matching these patterns are "
                            "auto-approved without prompting."
                        ),
                        "suggestion": "Review and remove unneeded entries.",
                    },
                },
            },
        ],
    },
    {
        "file": "{workspace}/.cursor/permissions.json",
        "label": "Workspace permissions",
        "optional": True,
        "properties": [
            {
                "key": "approvalMode",
                "ok": ["manual", "allowlist"],
                "critical": {
                    "unrestricted": {
                        "risk": (
                            "Workspace permissions.json overrides approval mode "
                            "to unrestricted for this project."
                        ),
                        "suggestion": (
                            'Set "approvalMode": "manual", or remove the file '
                            "to use the default."
                        ),
                    },
                },
            },
            {
                "key": "terminalAllowlist",
                "type": "non_empty_list",
                "warning": {
                    "non_empty": {
                        "risk": (
                            "Terminal commands matching these patterns are "
                            "auto-approved without prompting."
                        ),
                        "suggestion": "Review and remove unneeded entries.",
                    },
                },
            },
            {
                "key": "mcpAllowlist",
                "type": "non_empty_list",
                "warning": {
                    "non_empty": {
                        "risk": (
                            "MCP tool calls matching these patterns are "
                            "auto-approved without prompting."
                        ),
                        "suggestion": "Review and remove unneeded entries.",
                    },
                },
            },
        ],
    },
]


def main():
    parser = argparse.ArgumentParser(
        description="Check Cursor configuration for unsafe settings."
    )
    parser.add_argument(
        "--workspace",
        default=os.getcwd(),
        help="path to workspace (default: current directory)",
    )
    args = parser.parse_args()

    try:
        width = os.get_terminal_size().columns
    except OSError:
        width = 80

    text_width = width - YAML_INDENT

    summary = {OK: 0, CRITICAL: 0, WARNING: 0, INFO: 0}
    report = {"files": []}

    for check in CHECKS:
        path = check["file"].replace("{workspace}", args.workspace)
        label = check["label"]
        optional = check.get("optional", False)

        file_entry = {"file": path, "name": label, "checks": []}

        data = load_json(path)
        if data is None:
            sev = OK if optional else WARNING
            file_entry["checks"].append(
                {
                    "name": "file",
                    "value": "not found",
                    "status": _STATUSES[sev],
                }
            )
            summary[sev] += 1
            report["files"].append(file_entry)
            continue

        for prop in check["properties"]:
            severity, result = check_property(data, prop, text_width)
            file_entry["checks"].append(result)
            summary[severity] += 1

        report["files"].append(file_entry)

    report["summary"] = {
        _STATUSES[sev]: summary[sev]
        for sev in (OK, CRITICAL, WARNING, INFO)
        if summary[sev]
    }

    output = yaml.safe_dump(report, sort_keys=False, width=width, allow_unicode=True)

    print(output, end="")

    if summary[CRITICAL]:
        return 2
    if summary[WARNING]:
        return 1
    return 0


def check_property(data, prop, text_width):
    """Check a single property, return a dict for the report."""
    key = prop["key"]
    value = resolve_key(data, key)
    check_type = prop.get("type")

    if check_type == "non_empty_list":
        items = value if isinstance(value, list) else []
        match_key = "non_empty" if items else "empty"

        for severity in _SEVERITIES:
            rules = prop.get(severity, {})
            if match_key in rules:
                entry = rules[match_key]
                result = {
                    "name": key,
                    "value": format_value(items),
                    "status": _STATUSES[severity],
                    "risk": wrap_text(entry["risk"], text_width),
                    "suggestion": wrap_text(
                        entry["suggestion"], text_width
                    ),
                }
                return severity, result

        return OK, {"name": key, "value": format_value(items), "status": _STATUSES[OK]}

    ok_values = prop.get("ok", [])
    if value in ok_values:
        return OK, {"name": key, "value": format_value(value), "status": _STATUSES[OK]}

    for severity in _SEVERITIES:
        rules = prop.get(severity, {})
        if value in rules:
            entry = rules[value]
            result = {
                "name": key,
                "value": format_value(value),
                "status": _STATUSES[severity],
                "risk": wrap_text(entry["risk"], text_width),
                "suggestion": wrap_text(
                    entry["suggestion"], text_width
                ),
            }
            return severity, result

    return OK, {"name": key, "value": format_value(value), "status": _STATUSES[OK]}


def load_json(path):
    try:
        with open(path) as f:
            return json.load(f)
    except FileNotFoundError:
        return None
    except json.JSONDecodeError as e:
        print(f"Failed to parse {path}: {e}", file=sys.stderr)
        return None


def resolve_key(data, key):
    """Resolve a dot-separated key path in a dict."""
    parts = key.split(".")
    value = data
    for part in parts:
        if not isinstance(value, dict):
            return None
        value = value.get(part)
        if value is None:
            return None
    return value


def format_value(value):
    if isinstance(value, list):
        if not value:
            return "empty"
        return value
    return value


def wrap_text(text, width):
    """Wrap text to width, preserving existing line breaks."""
    lines = []
    for paragraph in text.split("\n"):
        lines.extend(
            textwrap.wrap(
                paragraph, width=width, break_on_hyphens=False
            )
            or [""]
        )
    return "\n".join(lines)


def _str_presenter(dumper, data):
    """
    Preserve multiline strings when dumping yaml.
    https://github.com/yaml/pyyaml/issues/240
    """
    if "\n" in data:
        block = "\n".join([line.rstrip() for line in data.splitlines()])
        if data.endswith("\n"):
            block += "\n"
        return dumper.represent_scalar("tag:yaml.org,2002:str", block, style="|")
    return dumper.represent_scalar("tag:yaml.org,2002:str", data)


yaml.representer.SafeRepresenter.add_representer(str, _str_presenter)


if __name__ == "__main__":
    sys.exit(main())
