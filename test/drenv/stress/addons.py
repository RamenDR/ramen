# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Analyze addon durations across stress test runs.

Parses log files to extract addon start/test completion times and generates
a box plot showing the distribution of each addon's duration across all runs.
This helps identify addons with high variability or anomalous behavior.
"""

import io
import json
import logging
import os
import re
from collections import defaultdict

log = logging.getLogger("addons")

_PATTERN = re.compile(
    r"\[(\w+)/\d+\] (addons/\S+/(?:start|test)) (?:completed|failed) in (\d+\.\d+) seconds"
)


def analyze(directory):
    """
    Analyze addon durations and generate a box plot and outliers report.

    Parses all run log files, extracts addon completion times, creates
    a box plot showing duration distributions, and returns a markdown
    section with the plot and an outliers table.

    Args:
        directory: Path to the test output directory containing log files
            and test.json.

    Returns:
        Markdown string with addon analysis, or None if skipped.
    """
    test_json_path = os.path.join(directory, "test.json")
    if not os.path.isfile(test_json_path):
        log.warning("No test.json found, skipping addon analysis")
        return None

    with open(test_json_path) as f:
        test = json.load(f)

    results = test.get("results", [])
    if not results:
        log.warning("No results in test.json, skipping addon analysis")
        return None

    addons = _parse_logs(directory, results)
    if not addons:
        log.warning("No addon durations found in logs")
        return None

    log.info("Found %d unique addons", len(addons))

    png_path = os.path.join(directory, "addons.png")
    _plot(addons, len(results), png_path)
    log.info("Addon durations plot: %s", png_path)

    return _format_report(addons)


def _parse_logs(directory, results):
    """
    Parse log files and extract addon durations.

    Returns dict mapping addon name -> list of (run, duration) tuples.
    Symmetric DR clusters (dr1, dr2) are merged into "dr".
    """
    addons = defaultdict(list)

    for result in results:
        run = result["name"]
        log_path = os.path.join(directory, run + ".log")
        if not os.path.isfile(log_path):
            continue

        with open(log_path) as f:
            for line in f:
                m = _PATTERN.search(line)
                if m:
                    cluster = m.group(1)
                    addon = m.group(2).removeprefix("addons/")
                    duration = float(m.group(3))
                    # Merge dr1/dr2 into "dr" since they are symmetric.
                    if cluster.startswith("dr"):
                        cluster = "dr"
                    key = f"{cluster} {addon}"
                    addons[key].append((run, duration))

    return addons


def _plot(addons, num_runs, png_path):
    """
    Create a horizontal box plot of addon durations.

    Addons are sorted by median duration (slowest at top).
    """
    # Imported locally to avoid slowing down all drenv commands.
    import matplotlib
    import matplotlib.pyplot as plt

    matplotlib.use("Agg")

    sorted_addons = sorted(
        addons.items(),
        key=lambda item: sorted(d for _, d in item[1])[len(item[1]) // 2],
    )

    names = [name for name, _ in sorted_addons]
    data = [[d for _, d in durations] for _, durations in sorted_addons]

    fig, ax = plt.subplots(figsize=(14, max(8, len(names) * 0.4)))

    ax.boxplot(
        data,
        vert=False,
        tick_labels=names,
        patch_artist=True,
        flierprops=dict(marker="o", markersize=5, markerfacecolor="red"),
        boxprops=dict(facecolor="lightblue", edgecolor="black"),
        medianprops=dict(color="darkblue", linewidth=1.5),
    )

    ax.set_xlabel("Duration (seconds)")
    ax.set_title(f"Addon durations across {num_runs} stress test runs")
    ax.grid(axis="x", alpha=0.3)

    plt.tight_layout()
    fig.savefig(png_path, dpi=150)
    plt.close()


def _find_outliers(durations):
    """
    Find outlier data points using the IQR method (1.5x IQR from quartiles).

    Returns list of (run, duration) tuples for outlier points.
    """
    values = sorted(d for _, d in durations)
    q1 = values[len(values) // 4]
    q3 = values[3 * len(values) // 4]
    iqr = q3 - q1
    upper = q3 + 1.5 * iqr
    lower = q1 - 1.5 * iqr

    return [(run, d) for run, d in durations if d > upper or d < lower]


def _format_report(addons):
    """
    Format addon analysis as markdown with a box plot and outliers table.
    """
    out = io.StringIO()
    out.write("## Addon Durations\n\n")
    out.write("![Addon Durations](addons.png)\n\n")

    # Collect addons with meaningful outliers, sorted by median (slowest first).
    # Skip outliers that differ from the median by less than 5 seconds since
    # they don't meaningfully affect overall timing.
    addon_outliers = []
    for name, durations in addons.items():
        outliers = _find_outliers(durations)
        if not outliers:
            continue
        values = sorted(d for _, d in durations)
        median = values[len(values) // 2]
        significant = [(r, d) for r, d in outliers if abs(d - median) >= 5]
        if significant:
            addon_outliers.append((name, median, significant))

    if not addon_outliers:
        return out.getvalue()

    addon_outliers.sort(key=lambda x: -x[1])

    out.write("### Outliers\n\n")
    out.write("| Addon | Median | Count | Range | Runs (sorted by time) |\n")
    out.write("|-------|-------:|------:|------:|------|\n")

    for name, median, outliers in addon_outliers:
        count = len(outliers)
        durations = sorted(d for _, d in outliers)
        lo, hi = durations[0], durations[-1]
        runs = ", ".join(run for run, _ in sorted(outliers, key=lambda x: x[1]))
        out.write(
            f"| {name} | {median:.0f}s | {count} | {lo:.0f}–{hi:.0f}s | {runs} |\n"
        )

    return out.getvalue()
