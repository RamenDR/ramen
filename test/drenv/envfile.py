# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import copy

import yaml


class MissingAddon(Exception):
    pass


def load(fileobj, name_prefix=None, addons_root="addons"):
    env = yaml.safe_load(fileobj)

    _validate_env(env, addons_root)

    if name_prefix:
        _prefix_names(env, name_prefix)

    return env


def _validate_env(env, addons_root):
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
        _validate_profile(profile, addons_root)

    for i, worker in enumerate(env["workers"]):
        _validate_worker(worker, env, addons_root, i)


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


def _validate_profile(profile, addons_root):
    if "name" not in profile:
        raise ValueError("Missing profile name")

    # If True, this is an external cluster and we don't have to start it.
    profile.setdefault("external", False)

    # Properties for minikube created cluster.
    profile.setdefault("driver", "kvm2")
    profile.setdefault("container_runtime", "")
    profile.setdefault("extra_disks", 0)
    profile.setdefault("disk_size", "20g")
    profile.setdefault("nodes", 1)
    profile.setdefault("cni", "auto")
    profile.setdefault("cpus", 2)
    profile.setdefault("memory", "4g")
    profile.setdefault("network", "")
    profile.setdefault("addons", [])
    profile.setdefault("workers", [])

    for i, worker in enumerate(profile["workers"]):
        _validate_worker(worker, profile, addons_root, i)


def _validate_worker(worker, env, addons_root, index):
    worker["name"] = f'{env["name"]}/{worker.get("name", index)}'
    worker.setdefault("addons", [])

    for addon in worker["addons"]:
        _validate_addon(addon, env, addons_root, args=[env["name"]])


def _validate_addon(addon, env, addons_root, args=()):
    if "name" not in addon:
        raise ValueError(f"Missing addon 'name': {addon}")

    addon_dir = os.path.join(addons_root, addon["name"])
    if not os.path.isdir(addon_dir):
        raise MissingAddon(addon["name"])

    args = addon.setdefault("args", list(args))

    for i, arg in enumerate(args):
        arg = arg.replace("$name", env["name"])
        args[i] = arg


def _prefix_names(env, name_prefix):
    profile_names = {p["name"] for p in env["profiles"]}

    env["name"] = name_prefix + env["name"]

    for profile in env["profiles"]:
        profile["name"] = name_prefix + profile["name"]
        for worker in profile["workers"]:
            _prefix_worker(worker, profile_names, name_prefix)

    for worker in env["workers"]:
        _prefix_worker(worker, profile_names, name_prefix)


def _prefix_worker(worker, profile_names, name_prefix):
    worker["name"] = name_prefix + worker["name"]

    for addon in worker["addons"]:
        args = addon["args"]
        for i, value in enumerate(args):
            if value in profile_names:
                args[i] = name_prefix + value
