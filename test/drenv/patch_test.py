# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from drenv import patch


def test_empty():
    assert patch.merge({}, {}) == {}


def test_add_key():
    a = {"key1": "old-key"}
    b = {"key2": "new-key"}
    assert patch.merge(a, b) == {"key1": "old-key", "key2": "new-key"}


def test_replace_key():
    a = {"key": "old-value"}
    b = {"key": "new-value"}
    assert patch.merge(a, b) == {"key": "new-value"}


def test_add_dict():
    a = {"key1": "old-key"}
    b = {"key2": {"key": "new-key"}}
    assert patch.merge(a, b) == {"key1": "old-key", "key2": {"key": "new-key"}}


def test_merge_dict():
    a = {"key": {"key1": "old-key"}}
    b = {"key": {"key2": "new-key"}}
    assert patch.merge(a, b) == {"key": {"key1": "old-key", "key2": "new-key"}}


def test_merge_recursive():
    a = {
        "key1": {
            "key1": "old-value",
            "key2": {
                "key1": "old-key",
            },
        },
    }
    b = {
        "key1": {
            "key1": "new-value",
            "key2": {
                "key2": "new-key",
            },
            "key3": "new-key",
        },
        "key2": "new-key",
    }
    assert patch.merge(a, b) == {
        "key1": {
            "key1": "new-value",
            "key2": {
                "key1": "old-key",
                "key2": "new-key",
            },
            "key3": "new-key",
        },
        "key2": "new-key",
    }


def test_merge_str():
    assert patch.merge("a", "b") == "b"
    assert patch.merge("a", None) == "a"


def test_merge_number():
    assert patch.merge(1, 2) == 2
    assert patch.merge(3.1, 3.14) == 3.14
    assert patch.merge(1, None) == 1


def test_merge_list():
    assert patch.merge(["a", "b"], ["b", "c"]) == ["b", "c"]
    assert patch.merge(["a", "b"], None) == ["a", "b"]


def test_merge_bool():
    assert patch.merge(True, False) is False
    assert patch.merge(False, True) is True
    assert patch.merge(False, None) is False
