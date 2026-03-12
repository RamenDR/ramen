# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Collect system metrics from Netdata for a stress test run.

Netdata is a real-time performance monitoring tool that collects system
metrics (CPU, memory, disk, network, etc.) and stores them with historical
data. This module exports metrics for a specific time range corresponding
to a stress test run.

The module determines the time range from test.json timestamps if available,
or computes it from file modification times for older test results.

Requirements:

- Netdata must be running locally (http://localhost:19999)
- Netdata must have historical data for the test time range

If Netdata is not available, metrics collection is skipped.
"""

import json
import logging
import os
import urllib.error
import urllib.request

log = logging.getLogger("metrics")

NETDATA_URL = "http://localhost:19999"

# Buffer time (seconds) added before start and after end to capture
# system state before/after the test runs (10 minutes each side).
TIME_BUFFER = 600

# Charts to export - these are preferences, actual charts are filtered
# based on what's available in the Netdata instance.
PREFERRED_CHARTS = [
    # System-wide metrics
    "system.cpu",
    "system.ram",
    "system.io",
    "system.load",
    "system.swap",
    "system.processes",
    "system.ctxt",
    "system.intr",
    "system.net",
    # Memory metrics
    "mem.swap",
    "mem.pgfaults",
    # Per-application metrics (Linux only, requires apps.plugin)
    "apps.cpu",
    "apps.mem",
    "apps.io_read",
    "apps.io_write",
    "apps.processes",
]

# Prefixes for auto-discovered charts (network, disk interfaces).
AUTO_DISCOVER_PREFIXES = [
    "net.",
    "disk.",
]


def collect(directory):
    """
    Collect Netdata metrics for a stress test run.

    Creates a metrics/ subdirectory containing CSV files for each chart
    and a metrics.json with time range and chart list.

    If Netdata is not available, logs a warning and returns False.

    Args:
        directory: Path to directory containing test.json.

    Returns:
        True if metrics were collected, False if skipped.
    """
    test_json_path = os.path.join(directory, "test.json")
    with open(test_json_path) as f:
        test_json = json.load(f)

    start_time, end_time, source = _get_time_range(test_json, directory, test_json_path)
    log.info("Time range: %d seconds (from %s)", end_time - start_time, source)

    available_charts = _get_available_charts()
    if not available_charts:
        log.warning(
            "Netdata is not available at %s, skipping metrics collection",
            NETDATA_URL,
        )
        return False

    charts = _select_charts(available_charts)
    log.info("Exporting %d charts", len(charts))

    metrics_dir = os.path.join(directory, "metrics")
    os.makedirs(metrics_dir, exist_ok=True)

    for chart in charts:
        _export_chart(chart, start_time, end_time, metrics_dir)

    metadata = {
        "start_time": start_time,
        "end_time": end_time,
        "duration_seconds": end_time - start_time,
        "charts": charts,
    }
    metadata_file = os.path.join(metrics_dir, "metrics.json")
    with open(metadata_file, "w") as f:
        json.dump(metadata, f, indent=2)
        f.write("\n")

    log.info("Metrics exported to: %s", metrics_dir)
    return True


def _get_time_range(test_json, directory, test_json_path):
    """
    Get start and end timestamps for the test run.

    Tries to read start_time and end_time from test.json. For older test
    results without these fields, falls back to computing the time range
    from file modification times and run durations.

    A buffer (TIME_BUFFER) is added before and after to capture metrics
    during environment setup and teardown.

    Returns:
        Tuple of (start_time, end_time, source).
    """
    start_time = test_json.get("start_time")
    end_time = test_json.get("end_time")

    if start_time and end_time:
        return int(start_time) - TIME_BUFFER, int(end_time) + TIME_BUFFER, "test.json"

    # TODO: Remove this fallback once all test.json files have timestamps.
    return _get_time_range_fallback(test_json, directory, test_json_path)


def _get_time_range_fallback(test_json, directory, test_json_path):
    """
    Compute time range from file timestamps for old test.json without timestamps.

    The algorithm:
        - end_time: test.json modification time + buffer
        - start_time: first log file mtime - first run duration - buffer
    """
    end_time = int(os.stat(test_json_path).st_mtime) + TIME_BUFFER

    results = test_json.get("results", [])
    if results:
        first_run = results[0]
        first_log = os.path.join(directory, first_run["name"] + ".log")
        if os.path.isfile(first_log):
            first_log_mtime = int(os.stat(first_log).st_mtime)
            first_run_duration = first_run.get("time", 0)
            start_time = int(first_log_mtime - first_run_duration - TIME_BUFFER)
            return start_time, end_time, "computed from first run"

    return end_time, end_time, "file system"


def _get_available_charts():
    """
    Fetch list of available charts from Netdata.

    Returns:
        Set of chart names, or empty set if Netdata is not reachable.
    """
    url = f"{NETDATA_URL}/api/v1/charts"
    try:
        with urllib.request.urlopen(url, timeout=10) as response:
            data = json.load(response)
            return set(data.get("charts", {}).keys())
    except (urllib.error.URLError, json.JSONDecodeError, OSError) as e:
        log.debug("Cannot fetch charts from %s: %s", NETDATA_URL, e)
        return set()


def _select_charts(available_charts):
    """
    Select charts to export based on what's available.

    Filters PREFERRED_CHARTS to only include charts that exist on the
    current system, and auto-discovers network and disk interface charts
    which have platform-specific names.
    """
    charts = []

    for chart in PREFERRED_CHARTS:
        if chart in available_charts:
            charts.append(chart)

    for prefix in AUTO_DISCOVER_PREFIXES:
        for chart in sorted(available_charts):
            if chart.startswith(prefix) and chart not in charts:
                charts.append(chart)

    return charts


def _export_chart(chart, start_time, end_time, metrics_dir):
    """
    Export a single chart from Netdata to a CSV file.
    """
    url = (
        f"{NETDATA_URL}/api/v1/data"
        f"?chart={chart}"
        f"&after={start_time}"
        f"&before={end_time}"
        f"&format=csv"
    )

    filename = f"{chart.replace('.', '_')}.csv"
    filepath = os.path.join(metrics_dir, filename)

    # If metrics were already collected, we keep the collected data. This is
    # useful for developemnt in 2 cases:
    # 1. Running report on another machine - metrics cannnot be collected.
    # 2. Running report to update plot output during development.
    if os.path.exists(filepath):
        log.debug(f"Chart {chart} exists, skipping")
        return

    try:
        with urllib.request.urlopen(url, timeout=30) as response:
            data = response.read()
            with open(filepath, "wb") as f:
                f.write(data)
        log.debug("Exported: %s", chart)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            log.debug("Skipped: %s - no data for time range", chart)
        else:
            log.warning("Failed to export %s: %s", chart, e)
    except urllib.error.URLError as e:
        log.warning("Failed to export %s: %s", chart, e)
