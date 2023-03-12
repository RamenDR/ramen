# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import json
import secrets

from drenv import kubectl


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
    kubectl.apply("--filename=example/deployment.yaml", context=tmpenv.profile)
    out, err = capsys.readouterr()
    assert out.strip() == "deployment.apps/example-deployment unchanged"


def test_rollout(tmpenv, capsys):
    kubectl.rollout(
        "status",
        "deploy/example-deployment",
        "--timeout=10s",
        context=tmpenv.profile,
    )
    out, err = capsys.readouterr()
    assert out.strip() == 'deployment "example-deployment" successfully rolled out'


def test_wait(tmpenv, capsys):
    kubectl.wait(
        "deploy/example-deployment",
        "--for=condition=available",
        "--timeout=10s",
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
