# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

"""
Sentinel types for default-argument detection.
"""


class Duration(float):
    """
    Subclass of float so a default value is distinguishable from a caller-passed
    int or float when using 'is'. Avoids relying on implementation details of
    integer or float interning.
    """

    pass
