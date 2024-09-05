# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import platform
import shutil

from . import commands

BROKER_INFO = "broker-info.subm"


def deploy_broker(context, globalnet=False, broker_info=None, version=None, log=print):
    """
    Run subctl deploy-broker ... logging progress messages.

    If broker_info is not specified, a BROKER_INFO file is created in the
    current directory. Otherwise the file is moved to the specified path.
    """
    args = ["deploy-broker", "--context", context]
    if globalnet:
        args.append("--globalnet")
    if version:
        args.append(f"--version={version}")

    _watch(*args, log=log)

    if broker_info:
        shutil.move(BROKER_INFO, broker_info)


def join(broker_info, context, clusterid, cable_driver=None, version=None, log=print):
    """
    Run subctl join ... logging progress messages.
    """
    args = [
        "join",
        broker_info,
        "--context",
        context,
        "--clusterid",
        clusterid,
    ]
    if cable_driver:
        args.extend(("--cable-driver", cable_driver))
    if version:
        args.append(f"--version={version}")
    if platform.system().lower() == "darwin":
        args.append("--check-broker-certificate=false")
    _watch(*args, log=log)


def export(what, name, context, namespace=None, log=print):
    """
    Run subctl export ... logging progress messages.
    """
    args = ["export", what, "--context", context]
    if namespace:
        args.extend(("--namespace", namespace))
    args.append(name)
    _watch(*args, log=log)


def unexport(what, name, context, namespace=None, log=print):
    """
    Run subctl unexport ... logging progress messages.
    """
    args = ["unexport", what, "--context", context]
    if namespace:
        args.extend(("--namespace", namespace))
    args.append(name)
    _watch(*args, log=log)


def show(what, context):
    return commands.run("subctl", "show", what, "--context", context)


def _watch(cmd, *args, context=None, log=print):
    cmd = ["subctl", cmd, *args]
    for line in commands.watch(*cmd):
        log(line)
