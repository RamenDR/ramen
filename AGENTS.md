# AGENTS.md

Guidance for AI coding agents and contributors on how to shape pull requests and commits in this repository.

## PR & commit expectations

Structure your PR as a series of small, atomic commits where each one is a single logical change that builds and passes tests on its own — don't lump unrelated fixes together, and don't split one change across many "wip" commits that you expect to be squashed. Write commit messages as a real explanation, not a label: a concise imperative subject line (≤ ~72 chars, e.g. `hub: fix VRG namespace detection for merge generators`) prefixed with the affected component, a blank line, then a body that says *why* the change is needed and what the reader should know — not just *what* the diff already shows. Sign off every commit with `git commit -s` (DCO), reference the relevant GitHub issue or DFBUGS JIRA where applicable, and rebase onto the latest target branch rather than merging it in so history stays linear. Before opening the PR, review your own diff commit-by-commit as if you were the reviewer: no leftover debug output, no unrelated churn, tests and lint green, and a PR description that gives context for the whole change and how you verified it.
