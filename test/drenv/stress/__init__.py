# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

from .run import command as run
from .compare import command as compare
from .report import command as report

__all__ = ["run", "compare", "report"]
