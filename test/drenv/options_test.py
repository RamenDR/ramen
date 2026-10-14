# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import options

# wrap_description


def test_wrap_description_width_shorter_than_input():
    # Real registry-cache description, terminal narrower than the text.
    text = "Manage the drenv registry cache. Inspect or remove cached registry containers used by drenv."
    result = options.wrap_description(text, width=40)
    assert result == """\
Manage the drenv registry cache. Inspect
or remove cached registry containers
used by drenv."""


def test_wrap_description_width_longer_than_input():
    # Real registry-cache description, terminal wider than the text — no wrapping.
    text = "Manage the drenv registry cache. Inspect or remove cached registry containers used by drenv."
    result = options.wrap_description(text, width=120)
    assert result == text


def test_wrap_description_normal_paragraph_before_indented_block():
    # Real stats command description, terminal narrower than the paragraph — paragraph wrapped, Examples block unchanged.
    text = """\
Display the current contents and usage statistics for the cached registry containers managed by drenv.

Examples:
  # Show cache statistics in JSON format
  drenv registry-cache stats"""
    result = options.wrap_description(text, width=60)
    expected = """\
Display the current contents and usage statistics for the
cached registry containers managed by drenv.

Examples:
  # Show cache statistics in JSON format
  drenv registry-cache stats"""
    assert result == expected


def test_wrap_description_empty_string():
    assert options.wrap_description("", width=80) == ""
