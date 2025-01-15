# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
import logging

# Map zap log levels to python log levels.
# https://github.com/uber-go/zap/blob/6d482535bdd97f4d97b2f9573ac308f1cf9b574e/zapcore/level.go#L113
_LOGGERS = {
    "debug": logging.debug,
    "info": logging.info,
    "warn": logging.warn,
    "error": logging.error,
    "dpanic": logging.error,
    "panic": logging.error,
    "fatal": logging.error,
}


def log_json(line, name="zap"):
    info = json.loads(line)
    info.pop("ts", None)
    msg = info.pop("msg", None)
    level = info.pop("level", None)
    logger = info.pop("logger", name)
    log = _LOGGERS.get(level.lower(), logging.info)
    if info:
        log("[%s] %s %s", logger, msg, info)
    else:
        log("[%s] %s", logger, msg)
