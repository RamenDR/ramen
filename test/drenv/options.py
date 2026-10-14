# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import argparse
import functools
import shutil
import textwrap


def terminal_width():
    """
    Return the terminal width, or default to 80 if not attached to a terminal.
    """
    return shutil.get_terminal_size(fallback=(80, 24)).columns


def formatter_class(width):
    """
    Formatter factory for argparse's `formatter_class=`.

    Works together with wrap_description(): that pre-wraps the
    description to the terminal width, and this gives the same width
    to RawDescriptionHelpFormatter so the rest of the help output
    (usage line, argument columns) wraps to match.
    """
    return functools.partial(argparse.RawDescriptionHelpFormatter, width=width)


def dedent(s):
    """
    Dedent and strip a multi-line string.
    """
    return textwrap.dedent(s).strip()


def wrap_description(text, width):
    """
    Wrap description/help text to the given width, filling normal paragraphs
    but preserving blank lines and indented blocks (e.g. an "Examples:"
    section) as-is.

    width must always be supplied explicitly — call terminal_width() once at
    startup and pass the result here so every description is wrapped at the
    same width and terminal_width() is never called more than once per run.

    Works together with formatter_class():
    - Here we pre-wrap the description so it matches the terminal width.
    - There we override width= when creating
      argparse.RawDescriptionHelpFormatter, so the rest of the help output
      (usage line, argument help columns) wraps to the same width.
    """
    paragraphs = text.split("\n\n")
    filled = []
    for paragraph in paragraphs:
        lines = paragraph.splitlines()
        has_indent = any(line[:1].isspace() for line in lines if line.strip())
        if has_indent:
            # Contains an indented block (e.g. example commands under an
            # "Examples:" header) - keep as is.
            filled.append(paragraph)
        else:
            filled.append(textwrap.fill(paragraph, width))
    return "\n\n".join(filled)
