# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import pytest

from drenv import options

# ---------------------------------------------------------------------------
# wrap_description
# ---------------------------------------------------------------------------

# --- single paragraph -------------------------------------------------------


def test_wrap_description_single_short_line():
    # A line shorter than width is returned unchanged.
    assert options.wrap_description("short line", width=40) == "short line"


def test_wrap_description_single_long_line():
    # A long line is wrapped at the given width.
    text = "word " * 20  # 100 chars
    result = options.wrap_description(text.strip(), width=40)
    for line in result.splitlines():
        assert len(line) <= 40


def test_wrap_description_respects_width():
    # Every output line must be within the requested width.
    words = ["longword"] * 10
    text = " ".join(words)
    result = options.wrap_description(text, width=30)
    for line in result.splitlines():
        assert len(line) <= 30


def test_wrap_description_fills_short_lines():
    # Two short lines that together fit in width are merged into one.
    text = "foo\nbar"
    result = options.wrap_description(text, width=80)
    assert result == "foo bar"


def test_wrap_description_single_word():
    # A single word shorter than width passes through unchanged.
    assert options.wrap_description("word", width=40) == "word"


def test_wrap_description_single_word_longer_than_width():
    # textwrap.fill hard-wraps a word longer than width at exactly width chars.
    long_word = "x" * 100
    result = options.wrap_description(long_word, width=40)
    for line in result.splitlines():
        assert len(line) <= 40


# --- multiple paragraphs ----------------------------------------------------


def test_wrap_description_two_paragraphs():
    # Paragraphs separated by a blank line are kept separate.
    text = "first paragraph text\n\nsecond paragraph text"
    result = options.wrap_description(text, width=80)
    assert result == "first paragraph text\n\nsecond paragraph text"


def test_wrap_description_three_paragraphs():
    # All three paragraphs survive, joined by blank lines.
    text = "one\n\ntwo\n\nthree"
    result = options.wrap_description(text, width=80)
    assert result == "one\n\ntwo\n\nthree"


def test_wrap_description_paragraph_separator_preserved():
    # Exactly one blank line (\n\n) separates each paragraph in the output.
    text = "para one long enough to maybe wrap\n\npara two also pretty long indeed"
    result = options.wrap_description(text, width=20)
    parts = result.split("\n\n")
    assert len(parts) == 2


def test_wrap_description_long_paragraphs_wrapped_independently():
    # Each paragraph is wrapped to the same width independently.
    words = "word " * 15
    text = words.strip() + "\n\n" + words.strip()
    result = options.wrap_description(text, width=30)
    parts = result.split("\n\n")
    assert len(parts) == 2
    for part in parts:
        for line in part.splitlines():
            assert len(line) <= 30


# --- indented blocks --------------------------------------------------------


def test_wrap_description_indented_block_preserved():
    # A paragraph containing indented lines (e.g. example commands) is kept
    # exactly as-is.
    text = "Examples:\n  drenv start envs/regional-dr.yaml"
    result = options.wrap_description(text, width=40)
    assert result == text


def test_wrap_description_indented_block_with_tab_preserved():
    # Tab-indented lines are also treated as indented.
    text = "Examples:\n\tdrenv start envs/foo.yaml"
    result = options.wrap_description(text, width=40)
    assert result == text


def test_wrap_description_indented_block_multiline_preserved():
    # Multiple consecutive indented lines in one paragraph are kept verbatim.
    text = (
        "Examples:\n"
        "  # Start regional-dr\n"
        "  drenv start envs/regional-dr.yaml\n"
        "  # Stop regional-dr\n"
        "  drenv stop envs/regional-dr.yaml"
    )
    result = options.wrap_description(text, width=40)
    assert result == text


def test_wrap_description_normal_paragraph_before_indented_block():
    # The normal paragraph is wrapped; the indented block is preserved.
    normal = "This is a normal paragraph that is long enough to be wrapped at the given width."
    indented = "Examples:\n  drenv start envs/regional-dr.yaml"
    text = normal + "\n\n" + indented
    result = options.wrap_description(text, width=40)
    parts = result.split("\n\n")
    assert len(parts) == 2
    for line in parts[0].splitlines():
        assert len(line) <= 40
    assert parts[1] == indented


def test_wrap_description_indented_block_before_normal_paragraph():
    # Order does not matter — the indented block is always preserved verbatim.
    indented = "Examples:\n  drenv start envs/regional-dr.yaml"
    normal = "This normal paragraph follows the examples block."
    text = indented + "\n\n" + normal
    result = options.wrap_description(text, width=40)
    parts = result.split("\n\n")
    assert parts[0] == indented


def test_wrap_description_blank_line_inside_indented_block_is_split():
    # wrap_description splits on \n\n, so a blank line inside an indented
    # block creates two separate paragraphs, each evaluated on its own merit.
    block1 = "Examples:\n  drenv start envs/foo.yaml"
    block2 = "  drenv stop envs/foo.yaml"
    text = block1 + "\n\n" + block2
    result = options.wrap_description(text, width=40)
    parts = result.split("\n\n")
    assert parts[0] == block1
    # block2 has indent so it is also preserved
    assert parts[1] == block2


# --- empty / whitespace-only input ------------------------------------------


def test_wrap_description_empty_string():
    # An empty string produces an empty string.
    assert options.wrap_description("", width=80) == ""


def test_wrap_description_only_spaces():
    # A paragraph of only spaces collapses to an empty line via textwrap.fill.
    result = options.wrap_description("   ", width=80)
    assert result == ""


def test_wrap_description_only_blank_lines():
    # Splitting "  \n\n  " on \n\n gives two space-only paragraphs, both
    # collapsed to "" by textwrap.fill, joined back with \n\n.
    result = options.wrap_description("\n\n", width=80)
    assert result == "\n\n"


# --- width boundary ---------------------------------------------------------


def test_wrap_description_width_exactly_fits_line():
    # A line whose length equals width is not broken.
    text = "a" * 40
    result = options.wrap_description(text, width=40)
    assert result == text


def test_wrap_description_width_one_over():
    # A line one character over the width must be broken (textwrap breaks
    # on whitespace, so we use two words that together exceed width).
    text = "hello world!"  # 12 chars
    result = options.wrap_description(text, width=10)
    lines = result.splitlines()
    assert len(lines) == 2
    assert lines[0] == "hello"
    assert lines[1] == "world!"


def test_wrap_description_width_one():
    # Width of 1 means every word gets its own line (textwrap never splits
    # a word mid-character).
    text = "a b c"
    result = options.wrap_description(text, width=1)
    assert result == "a\nb\nc"


# --- width is always required -----------------------------------------------


def test_wrap_description_width_required():
    # width is a required argument — omitting it raises TypeError immediately
    # rather than silently calling terminal_width() a second time.
    with pytest.raises(TypeError):
        options.wrap_description("some text")  # pylint: disable=no-value-for-parameter


def test_wrap_description_with_terminal_width(monkeypatch):
    # The canonical caller pattern: call terminal_width() once, then pass the
    # result to wrap_description.  Monkeypatching terminal_width() controls
    # the width used by the caller, not by wrap_description itself.
    monkeypatch.setattr("drenv.options.terminal_width", lambda: 30)
    width = options.terminal_width()
    text = "word " * 20
    result = options.wrap_description(text.strip(), width)
    for line in result.splitlines():
        assert len(line) <= 30
