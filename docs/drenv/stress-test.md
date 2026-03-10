<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# drenv stress test

The drenv stress test evaluates `drenv start` robustness and helps
debug failed runs.

## Setup

Before running stress tests install *Netdata* on the host to allow correlating
test failures with system load. See [metrics](/docs/drenv/metrics.md) for more
info.

## Running stress test

In this example we run 100 runs starting the regional-dr environment. If
a run fails, we delete the clusters and continue. This is useful for
understanding what are the most common failures.

```
drenv stress-test run -r 100 -o out/before envs/regional-dr.yaml
```

This creates the output directory, logging each run in a separate log
file, and saving test results in `test.json` file.

## Debugging a failed run

To stop after the first failure, leaving the clusters running for inspection,
use:

```
drenv stress-test run -r 100 -x envs/regional-dr.yaml
```

Because the failures are random, a run may fail very quickly or only
after many hours. As drenv becomes more reliable debugging random
failures will become harder.

> [!IMPORTANT]
> After debugging the failure, you need to delete the environment
> manually.

## Generating a report

After a test run, generate a report with failure analysis and system
metrics. The command collects metrics from Netdata (if installed) and
generates the report:

```
drenv stress-test report out
```

This creates `report.md` in the output directory with:

- Test configuration
- Tested code git info
- Host info
- Test stats
- Per-run results table
- Failure analysis
- Metrics info (if Netdata is installed)

## Comparing stress tests

Compare timing statistics from two stress test runs:

```
drenv stress-test compare out/before out/after
```

This prints a markdown table showing mean, median, min, max, and
standard deviation for both runs with the relative change.

## AI-assisted analysis

The generated reports are designed for AI consumption. Use an AI
assistant to analyze failures and metrics together:

**Suggested prompt:**

```
Analyze the stress test results in test/out/TEST_NAME/. Read report.md for test
info and error patterns, metrics/*.csv for system metrics during the test. If
errors lack sufficient detail, read the referenced log files. Correlate failures
with system metrics to identify load-related issues. Generate ai-analysis.md in
the same directory with prioritized action items.
```
