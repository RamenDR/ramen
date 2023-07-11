# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import json
import secrets

from drenv import kubectl

EXAMPLE_DEPLOYMENT = os.path.join("addons", "example", "deployment.yaml")

# Avoid random timeouts in github.
TIMEOUT = 30


def test_version(tmpenv):
    out = kubectl.version(output="json", context=tmpenv.profile)
    info = json.loads(out)
    # We care mostly about server version, but let's check also client version.
    assert "serverVersion" in info
    assert "clientVersion" in info


def test_get(tmpenv):
    out = kubectl.get("deploy", "--output=name", context=tmpenv.profile)
    assert out.strip() == "deployment.apps/example-deployment"


def test_config(tmpenv):
    out = kubectl.config("view", "--output=json")
    json.loads(out)


def test_exec(tmpenv):
    out = kubectl.exec(
        "deploy/example-deployment",
        "--",
        "hostname",
        context=tmpenv.profile,
    )
    assert out.startswith("example-deployment-")


def test_apply(tmpenv, capsys):
    kubectl.apply(f"--filename={EXAMPLE_DEPLOYMENT}", context=tmpenv.profile)
    out, err = capsys.readouterr()
    assert out.strip() == "deployment.apps/example-deployment unchanged"


def test_rollout(tmpenv, capsys):
    kubectl.rollout(
        "status",
        "deploy/example-deployment",
        timeout=TIMEOUT,
        context=tmpenv.profile,
    )
    out, err = capsys.readouterr()
    assert out.strip() == 'deployment "example-deployment" successfully rolled out'


def test_wait(tmpenv, capsys):
    kubectl.wait(
        resource="deploy/example-deployment",
        condition="condition=available",
        timeout=TIMEOUT,
        context=tmpenv.profile,
    )
    out, err = capsys.readouterr()
    assert out.strip() == "deployment.apps/example-deployment condition met"


def test_patch(tmpenv, capsys):
    pod = kubectl.get("pod", "--output=name", context=tmpenv.profile).strip()
    kubectl.patch(
        pod,
        "--type=merge",
        '--patch={"metadata": {"labels": {"test": "yes"}}}',
        context=tmpenv.profile,
    )
    out, err = capsys.readouterr()
    assert out.strip() == f"{pod} patched"


def test_label(tmpenv, capsys):
    pod = kubectl.get("pod", "--output=name", context=tmpenv.profile).strip()
    name = f"test-{secrets.token_hex(8)}"

    kubectl.label(pod, f"{name}=old", context=tmpenv.profile)
    out, err = capsys.readouterr()
    assert out.strip() == f"{pod} labeled"

    kubectl.label(pod, f"{name}=new", overwrite=True, context=tmpenv.profile)
    out, err = capsys.readouterr()
    assert out.strip() == f"{pod} labeled"


def test_delete(tmpenv, capsys):
    pod = kubectl.get("pod", "--output=name", context=tmpenv.profile).strip()
    kubectl.delete(pod, context=tmpenv.profile)
    out, err = capsys.readouterr()
    _, name = pod.split("/", 1)
    assert out.strip() == f'pod "{name}" deleted'
