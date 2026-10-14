# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Generate plots and metrics report from collected Netdata metrics.

Reads CSV files created by the metrics module and generates:

- PNG graphs for each metric
- ``metrics.md`` report with all charts organized by category
"""

import csv
import json
import logging
import os
from datetime import datetime

log = logging.getLogger("plot")

# Chart configurations: title, y-axis label, optional scale factor,
# and chart style ("stacked", "filled", or "line" default).
# Scale factor divides values for readability (e.g., KiB/s -> MiB/s).
CHART_CONFIG = {
    "system_cpu": {"title": "CPU Usage", "ylabel": "Percent", "style": "stacked"},
    "system_ram": {"title": "Memory Usage", "ylabel": "MiB", "style": "stacked"},
    "system_load": {"title": "System Load", "ylabel": "Load"},
    "system_io": {
        "title": "Disk I/O",
        "ylabel": "MiB/s",
        "scale": 1024,
        "style": "filled",
    },
    "system_net": {
        "title": "Network Traffic",
        "ylabel": "Mbit/s",
        "scale": 1000,
        "style": "filled",
    },
    "system_ctxt": {
        "title": "Context Switches",
        "ylabel": "Switches/s",
        "style": "filled",
    },
    "system_intr": {
        "title": "Interrupts",
        "ylabel": "Interrupts/s",
        "style": "filled",
    },
    "system_processes": {
        "title": "Processes",
        "ylabel": "Processes",
        "style": "filled",
    },
    "system_swap": {"title": "Swap Usage", "ylabel": "MiB"},
    "mem_swap": {"title": "Swap Usage", "ylabel": "MiB", "style": "stacked"},
    "mem_pgfaults": {"title": "Page Faults", "ylabel": "Faults/s"},
}

# Default configurations for chart name prefixes.
PREFIX_DEFAULTS = {
    "net_": {"ylabel": "Mbit/s", "scale": 1000, "style": "filled"},
    "disk_": {"ylabel": "MiB/s", "scale": 1024, "style": "filled"},
}

# Stacked area chart column ordering, per OS where needed.
# Each variant is a list of column names in visual order (top to bottom).
# The variant matching the most CSV columns is selected automatically.

_RAM_MACOS = [
    "free",
    "inactive",
    "purgeable",
    "speculative",
    "throttled",
    "compressor",
    "active",
    "wired",
]

_RAM_LINUX = [
    "free",
    "buffers",
    "cached",
    "used",
]

_CPU_MACOS = [
    "nice",
    "system",
    "user",
]

_CPU_LINUX = [
    "guest_nice",
    "steal",
    "irq",
    "softirq",
    "iowait",
    "nice",
    "user",
    "system",
    "guest",
]

_SWAP = [
    "free",
    "used",
]

STACKED_CHARTS = {
    "system_ram": [_RAM_MACOS, _RAM_LINUX],
    "system_cpu": [_CPU_MACOS, _CPU_LINUX],
    "mem_swap": [_SWAP],
}


def create_plots(directory):
    """
    Generate plots from collected metrics and a metrics.md report.

    If no metrics exist, logs a warning and returns False.

    Args:
        directory: Path to the test output directory.

    Returns:
        True if plots were created, False if skipped.
    """
    metrics_dir = os.path.join(directory, "metrics")
    if not os.path.isdir(metrics_dir):
        log.warning("No metrics directory found, skipping plots")
        return False

    csv_files = sorted(f for f in os.listdir(metrics_dir) if f.endswith(".csv"))
    if not csv_files:
        log.warning("No CSV files found in %s, skipping plots", metrics_dir)
        return False

    runs = _load_runs(directory)
    if runs:
        log.info("Loaded %d test runs for overlay", len(runs))

    log.info("Plotting %d charts", len(csv_files))

    created_charts = []
    for csv_file in csv_files:
        csv_path = os.path.join(metrics_dir, csv_file)
        chart_name = csv_file.replace(".csv", "")
        png_path = os.path.join(metrics_dir, chart_name + ".png")

        try:
            _plot_chart(csv_path, png_path, chart_name, runs=runs)
            log.debug("Created: %s.png", chart_name)
            created_charts.append(chart_name)
        except Exception as e:
            log.warning("Failed to plot %s: %s", chart_name, e)

    log.info("Plots saved to: %s", metrics_dir)

    metrics_md_path = os.path.join(directory, "metrics.md")
    _generate_metrics_md(metrics_md_path, created_charts)
    log.info("Metrics report: %s", metrics_md_path)

    return True


def _generate_metrics_md(path, charts):
    """
    Generate a markdown file with all metric charts.

    Charts are organized by category: System, Memory, Disk, Network.
    """
    system = []
    memory = []
    disk = []
    network = []

    for chart in sorted(charts):
        if chart.startswith("system_"):
            system.append(chart)
        elif chart.startswith("mem_"):
            memory.append(chart)
        elif chart.startswith("disk_"):
            disk.append(chart)
        elif chart.startswith("net_"):
            network.append(chart)

    with open(path, "w") as f:
        f.write("# System Metrics\n\n")

        if system:
            f.write("## System\n\n")
            for chart in system:
                title = _chart_title(chart)
                f.write(f"### {title}\n\n")
                f.write(f"![{title}](metrics/{chart}.png)\n\n")

        if memory:
            f.write("## Memory Details\n\n")
            for chart in memory:
                title = _chart_title(chart)
                f.write(f"### {title}\n\n")
                f.write(f"![{title}](metrics/{chart}.png)\n\n")

        if disk:
            f.write("## Disk\n\n")
            for chart in disk:
                title = _chart_title(chart)
                f.write(f"### {title}\n\n")
                f.write(f"![{title}](metrics/{chart}.png)\n\n")

        if network:
            f.write("## Network\n\n")
            for chart in network:
                title = _chart_title(chart)
                f.write(f"### {title}\n\n")
                f.write(f"![{title}](metrics/{chart}.png)\n\n")


def _get_config(chart_name):
    """Get chart configuration by exact name or prefix match."""
    config = CHART_CONFIG.get(chart_name)
    if config:
        return config
    for prefix, defaults in PREFIX_DEFAULTS.items():
        if chart_name.startswith(prefix):
            return defaults
    return {}


def _chart_title(chart_name):
    """Convert chart name to human-readable title."""
    config = _get_config(chart_name)
    if "title" in config:
        return config["title"]

    parts = chart_name.split("_", 1)
    if len(parts) == 2:
        category, name = parts
        category_map = {
            "system": "System",
            "mem": "Memory",
            "disk": "Disk",
            "net": "Network",
        }
        prefix = category_map.get(category, category.title())
        return f"{prefix} {name}"
    return chart_name.replace("_", " ").title()


def _select_variant(variants, columns):
    """
    Select the stacked chart variant matching the most CSV columns.
    """
    best = variants[0]
    best_count = 0
    for variant in variants:
        count = sum(1 for col in columns if col in variant)
        if count > best_count:
            best = variant
            best_count = count
    return best


def _apply_variant(variant, columns):
    """
    Order columns and assign colors for a stacked area chart.

    Variants are in visual order (top to bottom), reversed here for
    stackplot (bottom to top).
    """
    ordered = {}

    for name in reversed(variant):
        if name in columns:
            ordered[name] = columns[name]

    for name in columns:
        if name not in ordered:
            ordered[name] = columns[name]

    # Imported locally to avoid slowing down all drenv commands.
    import matplotlib

    tab10 = matplotlib.colormaps["tab10"].colors
    colors = [tab10[i % len(tab10)] for i in range(len(ordered))]

    return ordered, colors


def _load_runs(directory):
    """
    Load test run data for the run overlay bar.

    Returns run results with absolute timestamps, or None if test.json
    is missing or results lack timestamps.
    """
    test_json_path = os.path.join(directory, "test.json")
    if not os.path.isfile(test_json_path):
        return None

    with open(test_json_path) as f:
        test = json.load(f)

    results = test.get("results", [])
    if not results:
        return None

    # Need per-run timestamps to place runs on the time axis.
    if "start_time" not in results[0] or "end_time" not in results[0]:
        return None

    return results


def _plot_runs(ax, runs):
    """
    Draw a horizontal bar showing test runs with pass/fail colors.

    Each run is a colored rectangle (green=passed, red=failed) with
    run number labels. Labels are shown for every run when there is
    space, or every 5th run otherwise. Failed runs are always labeled.
    """
    pass_color = "#4caf50"
    fail_color = "#e53935"

    for run in runs:
        start = datetime.fromtimestamp(run["start_time"])
        end = datetime.fromtimestamp(run["end_time"])
        color = pass_color if run["passed"] else fail_color
        ax.axvspan(start, end, color=color)

    label_every = 1 if len(runs) <= 50 else 5

    for i, run in enumerate(runs):
        if not run["passed"] or i % label_every == 0:
            mid = datetime.fromtimestamp((run["start_time"] + run["end_time"]) / 2)
            label = run["name"].lstrip("0") or "0"
            ax.text(
                mid,
                0.5,
                label,
                ha="center",
                va="center",
                fontsize=7,
                transform=ax.get_xaxis_transform(),
            )

    ax.set_yticks([])
    ax.set_ylabel("Runs", fontsize=8)


def _plot_chart(csv_path, png_path, chart_name, runs=None):
    """
    Read a CSV file and create a PNG graph.

    The CSV format from Netdata has a "time" column (Unix timestamp or
    datetime string) and remaining columns with metric values. If runs
    are provided, a thin overlay bar showing pass/fail status is added
    at the top of each chart.
    """
    # Imported locally to avoid slowing down all drenv commands.
    import matplotlib.dates as mdates
    import matplotlib.pyplot as plt

    times = []
    columns = {}

    with open(csv_path) as f:
        reader = csv.DictReader(f)
        headers = reader.fieldnames

        if not headers or "time" not in headers:
            raise ValueError("CSV missing 'time' column")

        for header in headers:
            if header != "time":
                columns[header] = []

        for row in reader:
            time_str = row["time"]
            try:
                timestamp = float(time_str)
                times.append(datetime.fromtimestamp(timestamp))
            except ValueError:
                times.append(datetime.strptime(time_str, "%Y-%m-%d %H:%M:%S"))

            for header in headers:
                if header != "time":
                    try:
                        columns[header].append(float(row[header]))
                    except (ValueError, TypeError):
                        columns[header].append(0.0)

    if not times:
        raise ValueError("CSV has no data rows")

    config = _get_config(chart_name)
    title = _chart_title(chart_name)
    ylabel = config.get("ylabel", "Value")
    scale = config.get("scale", 1)

    if scale != 1:
        for label in columns:
            columns[label] = [v / scale for v in columns[label]]

    if runs:
        fig, (run_ax, ax) = plt.subplots(
            2,
            1,
            figsize=(24, 8.5),
            height_ratios=[1, 40],
            sharex=True,
        )
        _plot_runs(run_ax, runs)
        run_ax.set_title(title)
    else:
        fig, ax = plt.subplots(figsize=(24, 8))
        ax.set_title(title)

    style = config.get("style", "line")

    if style == "stacked":
        variant = _select_variant(STACKED_CHARTS[chart_name], columns)
        columns, colors = _apply_variant(variant, columns)
        labels = list(columns.keys())
        values = [columns[label] for label in labels]
        ax.stackplot(times, values, labels=labels, colors=colors)
    elif style == "filled":
        for label, values in columns.items():
            ax.fill_between(times, values, 0, label=label)
            ax.axhline(y=0, color="black", linewidth=0.5)
    else:
        for label, values in columns.items():
            ax.plot(times, values, label=label)

    ax.set_xlabel("Time")
    ax.set_ylabel(ylabel)

    if runs:
        for run in runs:
            ax.axvline(
                datetime.fromtimestamp(run["start_time"]),
                color="grey",
                linewidth=0.5,
                alpha=0.3,
            )
        ax.grid(True, axis="y", alpha=0.3)
    else:
        ax.grid(True, alpha=0.3)

    ax.ticklabel_format(style="plain", axis="y")

    ax.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M"))
    if runs:
        tick_times = [datetime.fromtimestamp(r["start_time"]) for r in runs]
        tick_step = max(1, len(runs) // 20)
        ax.set_xticks(tick_times[::tick_step])
    else:
        ax.xaxis.set_major_locator(mdates.AutoDateLocator())
    fig.autofmt_xdate()

    if len(columns) > 1:
        ax.legend(loc="upper left", fontsize="small", ncol=min(len(columns), 4))

    plt.tight_layout()
    plt.savefig(png_path, dpi=100)
    plt.close()
