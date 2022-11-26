# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import copy

import yaml


def load(filename):
    with open(filename) as f:
        env = yaml.safe_load(f)
    _validate_env(env)
    return env


def _validate_env(env):
    if "name" not in env:
        raise ValueError("Missing name")

    if "profiles" not in env:
        raise ValueError("Missing profiles")

    env.setdefault("templates", [])
    env.setdefault("workers", [])

    for template in env["templates"]:
        _validate_template(template)

    _bind_templates(env)

    for profile in env["profiles"]:
        _validate_profile(profile)

    for i, worker in enumerate(env["workers"]):
        _validate_worker(worker, env, i)


def _validate_template(template):
    if "name" not in template:
        raise ValueError("Missing template name")


def _bind_templates(env):
    templates = {t["name"]: t for t in env["templates"]}

    for i, profile in enumerate(env["profiles"]):
        # Ensure that profile is bound once.
        name = profile.pop("template", None)
        if name is None:
            continue

        if name not in templates:
            raise ValueError(f"Unknown template: {name}")

        # Deep copy the template so profiles do not share anything.
        template = copy.deepcopy(templates[name])

        # Merge template and profile, overiding template keys.
        env["profiles"][i] = {**template, **profile}


def _validate_profile(profile):
    if "name" not in profile:
        raise ValueError("Missing profile name")

    profile.setdefault("container_runtime", "containerd")
    profile.setdefault("extra_disks", 0)
    profile.setdefault("disk_size", "20g")
    profile.setdefault("nodes", 1)
    profile.setdefault("cni", "auto")
    profile.setdefault("cpus", 2)
    profile.setdefault("memory", "4g")
    profile.setdefault("network", "")
    profile.setdefault("scripts", [])
    profile.setdefault("addons", [])
    profile.setdefault("workers", [])

    for i, worker in enumerate(profile["workers"]):
        _validate_worker(worker, profile, i)


def _validate_worker(worker, env, index):
    worker.setdefault("name", f'{env["name"]}/{index}')
    worker.setdefault("scripts", [])

    for script in worker["scripts"]:
        _validate_script(script, env, args=[env["name"]])


def _validate_script(script, env, args=()):
    if "file" not in script:
        raise ValueError(f"Missing script 'file': {script}")

    args = script.setdefault("args", list(args))

    for i, arg in enumerate(args):
        arg = arg.replace("$name", env["name"])
        args[i] = arg
