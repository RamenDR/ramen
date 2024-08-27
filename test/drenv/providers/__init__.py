# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import importlib


def get(name):
    return importlib.import_module("drenv.providers." + name)
