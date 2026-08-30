# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from . import commands


def info():
    """
    Return git repository info as a dict with keys "branch", "commit", and
    "dirty".

    "dirty" is True if there are changes to tracked files (staged or
    unstaged); untracked files are ignored.

    Raises commands.Error if git is not installed, the current directory is
    not inside a git repository, or any git command fails.
    """
    branch = _rev_parse("HEAD", abbrev_ref=True)
    commit = _rev_parse("HEAD")
    # Any output means tracked files have changes; untracked files are
    # ignored with -uno.
    status = commands.run("git", "status", "--porcelain", "-uno").strip()
    dirty = status != ""

    return {"branch": branch, "commit": commit, "dirty": dirty}


def _rev_parse(ref=None, abbrev_ref=False):
    cmd = ["git", "rev-parse"]
    if abbrev_ref:
        cmd.append("--abbrev-ref")
    if ref:
        cmd.append(ref)
    return commands.run(*cmd).strip()
