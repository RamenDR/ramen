# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import logging
import shutil

from . import commands


def info():
    """
    Return git repository info as a dict with keys "branch", "commit", and
    "dirty", or an empty dict if git is not installed or the current
    directory is not inside a git repository.

    "dirty" is True if there are changes to tracked files (staged or
    unstaged); untracked files are ignored.
    """
    if not shutil.which("git"):
        logging.debug("[git] git is not installed")
        return {}

    if _rev_parse(is_inside_work_tree=True) != "true":
        logging.debug("[git] Not inside a git repo")
        return {}

    branch = _rev_parse("HEAD", abbrev_ref=True)
    commit = _rev_parse("HEAD")
    # Any output means tracked files have changes; untracked files are
    # ignored with -uno.
    status = commands.run("git", "status", "--porcelain", "-uno").strip()
    dirty = status != ""

    return {"branch": branch, "commit": commit, "dirty": dirty}


def _rev_parse(ref=None, abbrev_ref=False, is_inside_work_tree=False):
    cmd = ["git", "rev-parse"]
    if abbrev_ref:
        cmd.append("--abbrev-ref")
    if is_inside_work_tree:
        cmd.append("--is-inside-work-tree")
    if ref:
        cmd.append(ref)
    return commands.run(*cmd).strip()
