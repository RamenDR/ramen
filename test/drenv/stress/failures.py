# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Analyze failures from a stress test run.

For each failed run:

1. Find the log file (e.g., 000.log)
2. Parse the error block after "ERROR   Command failed"
3. Extract the structured drenv error (command, exitcode, error message)
4. Group similar errors by (addon, error_type) for the summary

Error types:

- ``bucket_creation`` - MinIO bucket creation failed (cluster network issue)
- ``timeout`` - Waiting for a Kubernetes resource timed out
- ``dns_failure`` - DNS lookup failed (CoreDNS not ready)
- ``firewall`` - macOS firewall blocking minikube
- ``cluster_start`` - Kubernetes cluster failed to start
- ``fatal`` - Generic fatal error
"""

import collections
import io
import json
import os
import re


def analyze(directory):
    """
    Analyze failures in a stress test output directory.

    Args:
        directory: Path to directory containing test.json and log files.

    Returns:
        Markdown string with failure analysis, or None if no failures.
    """
    test_json_path = os.path.join(directory, "test.json")
    with open(test_json_path) as f:
        test_json = json.load(f)

    failures = [r for r in test_json.get("results", []) if not r.get("passed", True)]
    if not failures:
        return None

    errors = []
    for failure in failures:
        log_path = os.path.join(directory, failure["name"] + ".log")
        error_info = extract_error(log_path)
        error_info["run"] = failure["name"]
        error_info["time"] = failure.get("time", 0)
        error_info["log"] = failure["name"] + ".log"
        errors.append(error_info)

    stats = test_json.get("stats", {})
    total = stats.get("runs", len(test_json.get("results", [])))

    out = io.StringIO()
    out.write("## Failure Analysis\n\n")
    out.write(
        f"**{len(failures)}/{total} runs failed ({len(failures) / total * 100:.1f}%)**\n\n"
    )
    _write_summary_table(out, errors)
    _write_detailed_errors(out, errors)

    return out.getvalue()


def extract_error(log_path):
    """
    Extract structured error information from a log file.

    Looks for the first "ERROR   Command failed" that occurred during
    drenv start (not during gather or delete phases). Extracts the
    addon name, failing command, and error type.

    Args:
        log_path: Path to the log file.

    Returns:
        Dict with keys: addon, command, error_type, error, raw_error.
    """
    default_result = {
        "addon": "unknown",
        "command": "unknown",
        "error_type": "unknown",
        "error": "Unknown error",
        "raw_error": "No error found",
    }

    if not os.path.isfile(log_path):
        default_result["error"] = "Log file not found"
        default_result["raw_error"] = "Log file not found"
        return default_result

    try:
        with open(log_path, "r", errors="replace") as f:
            content = f.read()
    except OSError as e:
        default_result["error"] = f"Cannot read log: {e}"
        default_result["raw_error"] = f"Cannot read log: {e}"
        return default_result

    # Find the first "ERROR   Command failed" block (from drenv start).
    # Later errors may be from gather/delete phases which are less relevant.
    error_marker = "ERROR   Command failed"
    pos = content.find(error_marker)
    if pos == -1:
        return _find_alternative_error(content)

    # Extract the full error block (up to next timestamp line).
    error_end = min(pos + 5000, len(content))
    next_log = re.search(
        r"\n\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2},\d{3}",
        content[pos + len(error_marker) :],
    )
    if next_log:
        error_end = pos + len(error_marker) + next_log.start()

    full_error_block = content[pos:error_end].strip()

    # Find the interesting part starting from "drenv.commands.Error:".
    # This skips the generic traceback and shows the actual command/error.
    error_block = _extract_drenv_error(full_error_block)

    addon, command = _extract_command(error_block)
    error_msg, error_type = _extract_key_error(error_block)

    return {
        "addon": addon,
        "command": command,
        "error_type": error_type,
        "error": error_msg or "Unknown error",
        "raw_error": error_block,
    }


def _extract_drenv_error(error_block):
    """
    Extract the useful part of the error starting from drenv.commands.Error.

    The full traceback includes generic drenv/__main__.py frames that are not
    helpful. The interesting part starts at:

        drenv.commands.Error: command failed:
          command:
          - addons/minio/start
          ...
    """
    match = re.search(r"(drenv\.commands\.Error:.*)", error_block, re.DOTALL)
    if match:
        return match.group(1)
    return error_block


def _extract_command(error_block):
    """
    Extract addon name and command from an error block.

    Looks for the drenv command pattern in the YAML error output:
        command:
        - addons/minio/start
        - dr1
    """
    # Match: "command:\n  - addons/ADDON/HOOK\n  - CLUSTER"
    match = re.search(
        r"command:\s*\n\s*-\s*(addons/([^/\s]+)/(\w+))\s*\n\s*-\s*(\S+)", error_block
    )
    if match:
        addon_path = match.group(1)
        addon_name = match.group(2)
        cluster = match.group(4)
        full_command = f"{addon_path} {cluster}"
        return addon_name, full_command

    # Check for minikube start failure (not addon-related).
    match = re.search(r"command:\s*\n\s*-\s*minikube", error_block)
    if match:
        cluster_match = re.search(r"\[(\w+)\] minikube", error_block)
        cluster = cluster_match.group(1) if cluster_match else "unknown"
        return "minikube", f"minikube start {cluster}"

    # Fallback: look for addon path in traceback.
    match = re.search(r"test/addons/([^/\s]+)/(\w+)", error_block)
    if match:
        return match.group(1), f"addons/{match.group(1)}/{match.group(2)}"

    return "unknown", "unknown"


def _find_alternative_error(content):
    """
    Find error when standard "ERROR   Command failed" is not present.

    Looks for alternative error patterns like firewall blocking bootpd,
    fatal messages, or other error indicators.
    """
    result = {
        "addon": "unknown",
        "command": "unknown",
        "error_type": "unknown",
        "error": "Unknown error",
        "raw_error": "No error block found",
    }

    # Firewall blocking bootpd (minikube on macOS).
    match = re.search(
        r"(Your firewall is blocking bootpd.*?)(\n\d{4}-\d{2}-\d{2}|\Z)",
        content,
        re.DOTALL,
    )
    if match:
        result["addon"] = "minikube"
        result["command"] = "minikube start"
        result["error_type"] = "firewall"
        result["error"] = "minikube: firewall blocking bootpd (requires sudo)"
        result["raw_error"] = match.group(1).strip()
        return result

    # Generic fatal error.
    match = re.search(r'level=fatal msg="([^"]+)"', content)
    if match:
        result["error_type"] = "fatal"
        result["error"] = f"fatal: {match.group(1)}"
        result["raw_error"] = match.group(0)
        return result

    # Any ERROR line - get surrounding context.
    match = re.search(r"(ERROR.{0,500})", content, re.DOTALL)
    if match:
        result["error"] = "Unknown error"
        result["raw_error"] = match.group(1).strip()
        return result

    return result


def _extract_key_error(error_block):
    """
    Extract the key error message and error type from an error block.

    Looks for specific patterns that indicate the actual error cause,
    such as mc errors, timeout messages, DNS failures, etc.

    Returns:
        Tuple of (error_message, error_type).
    """
    # mc bucket error - extract with the network error.
    match = re.search(
        r"mc: <ERROR> Unable to make bucket.*?dial tcp[^'\"]+", error_block, re.DOTALL
    )
    if match:
        error = match.group(0)
        error = re.sub(r"\s+", " ", error)
        return error, "bucket_creation"

    # Timeout waiting for condition.
    match = re.search(
        r"error: timed out waiting for the condition on (\S+)", error_block
    )
    if match:
        resource = match.group(1).rstrip("'\"")
        return f"timed out waiting for: {resource}", "timeout"

    # DNS/network errors.
    match = re.search(r"dial tcp: lookup (\S+).*?server misbehaving", error_block)
    if match:
        return f"DNS lookup failed: {match.group(1)}", "dns_failure"

    # Repository not accessible (ArgoCD/git).
    match = re.search(
        r"repository not accessible:.*?dial tcp.*?server misbehaving", error_block
    )
    if match:
        return "ArgoCD: repository not accessible (DNS failure)", "dns_failure"

    # Firewall/bootpd errors.
    match = re.search(r"firewall is blocking bootpd", error_block, re.IGNORECASE)
    if match:
        return "minikube: firewall blocking bootpd (requires sudo)", "firewall"

    # Context not found (cluster didn't start).
    match = re.search(
        r"context was not found for specified context: (\S+)", error_block
    )
    if match:
        return f"cluster failed to start: {match.group(1)}", "cluster_start"

    # Generic fatal error.
    match = re.search(r'level=fatal msg="([^"]+)"', error_block)
    if match:
        return f"fatal: {match.group(1)[:100]}", "fatal"

    return None, "unknown"


def _write_summary_table(out, errors):
    """Write a markdown table summarizing errors by addon and type."""
    error_groups = collections.defaultdict(list)
    for e in errors:
        key = (e["addon"], e["error_type"])
        error_groups[key].append(e)

    sorted_groups = sorted(error_groups.items(), key=lambda x: -len(x[1]))

    out.write("### Summary\n\n")
    out.write("| Count | Addon | Error Type | Runs |\n")
    out.write("|------:|-------|------------|------|\n")

    for (addon, error_type), group in sorted_groups:
        count = len(group)
        runs = ", ".join(e["run"] for e in group)
        out.write(f"| {count} | {addon} | {error_type} | {runs} |\n")

    out.write("\n")


def _write_detailed_errors(out, errors):
    """Write full error traceback for each failed run in markdown."""
    out.write("### Detailed Errors\n\n")

    for e in errors:
        out.write(f"#### Run {e['run']}\n\n")
        out.write(f"Log: `{e['log']}`\n\n")
        out.write("```\n")
        out.write(_truncate_drenv_error(e["raw_error"]))
        out.write("\n```\n\n")


MAX_HEAD_LINES = 10
MAX_TAIL_LINES = 10


def _truncate_drenv_error(raw_error):
    """
    Truncate drenv error, preserving command structure but trimming error text.

    The drenv error has a nested structure:

        drenv.commands.Error: command failed:
          command:
          - addon/name
          - arg1
          exitcode: 1
          error: |-
            Traceback (most recent call last):
              ...long traceback...
            drenv.commands.Error: command failed:
              command:
              - kubectl
              - wait
              exitcode: 1
              error: 'actual error message'

    We preserve the outer command and exitcode, trim the traceback before the
    inner drenv.commands.Error, and keep the inner error block intact.
    """
    error_match = re.search(r"^(\s*error:\s*)", raw_error, re.MULTILINE)
    if not error_match:
        return _truncate_lines(raw_error)

    error_start = error_match.start()
    error_field_start = error_match.end()

    header = raw_error[:error_start]
    error_prefix = error_match.group(1)
    error_content = raw_error[error_field_start:]

    # Check if there's a nested drenv.commands.Error inside the error content.
    inner_error_match = re.search(r"drenv\.commands\.Error:", error_content)
    if inner_error_match:
        traceback = error_content[: inner_error_match.start()]
        inner_error = error_content[inner_error_match.start() :]

        truncated_traceback = _truncate_lines(traceback.rstrip())
        truncated_inner = _truncate_drenv_error(inner_error)

        return header + error_prefix + truncated_traceback + "\n" + truncated_inner
    else:
        truncated_content = _truncate_lines(error_content)
        return header + error_prefix + truncated_content


def _truncate_lines(text):
    """Truncate text keeping the first and last lines."""
    lines = text.split("\n")

    if len(lines) <= MAX_HEAD_LINES + MAX_TAIL_LINES:
        return text

    head_lines = lines[:MAX_HEAD_LINES]
    tail_lines = lines[-MAX_TAIL_LINES:]
    trimmed_count = len(lines) - MAX_HEAD_LINES - MAX_TAIL_LINES

    indent = _detect_indent(head_lines)

    head = "\n".join(head_lines)
    tail = "\n".join(tail_lines)

    return f"{head}\n{indent}...<{trimmed_count} lines trimmed>...\n{tail}"


def _detect_indent(lines):
    """
    Detect the indentation level for the trim marker.

    Look for "File" lines in tracebacks to align with them, otherwise
    use the minimum non-zero indentation.
    """
    for line in lines:
        stripped = line.lstrip()
        if stripped.startswith('File "'):
            spaces = len(line) - len(stripped)
            return " " * spaces

    min_indent = None
    for line in lines:
        if not line.strip():
            continue

        spaces = len(line) - len(line.lstrip())
        if spaces > 0:
            if min_indent is None or spaces < min_indent:
                min_indent = spaces

    return " " * (min_indent if min_indent else 6)
