# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

import os
import json
import secrets
from contextlib import closing

import pytest

from drenv import commands
from drenv import kubectl

EXAMPLE_DEPLOYMENT = os.path.join("addons", "example", "deployment.yaml")

# Avoid random timeouts in github.
TIMEOUT = 30

pytestmark = pytest.mark.cluster


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
        f"--timeout={TIMEOUT}s",
        context=tmpenv.profile,
    )
    out, err = capsys.readouterr()
    assert out.strip() == 'deployment "example-deployment" successfully rolled out'


def test_wait(tmpenv, capsys):
    kubectl.wait(
        "deploy/example-deployment",
        "--for=condition=available",
        f"--timeout={TIMEOUT}s",
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


def test_annotate(tmpenv, capsys):
    pod = kubectl.get("pod", "--output=name", context=tmpenv.profile).strip()
    annotation = f"test-{secrets.token_hex(8)}"

    print(f"Adding new annotation {annotation}")
    kubectl.annotate(pod, {annotation: "old"}, context=tmpenv.profile)
    assert _get_annotations(pod, tmpenv.profile)[annotation] == "old"

    print(f"Overwirting annotation {annotation}")
    kubectl.annotate(
        pod,
        {annotation: "new"},
        overwrite=True,
        context=tmpenv.profile,
    )
    assert _get_annotations(pod, tmpenv.profile)[annotation] == "new"

    print(f"Removing annotation {annotation}")
    kubectl.annotate(pod, {annotation: None}, context=tmpenv.profile)
    assert annotation not in _get_annotations(pod, tmpenv.profile)


def test_delete(tmpenv, capsys):
    pod = kubectl.get("pod", "--output=name", context=tmpenv.profile).strip()
    kubectl.delete(pod, context=tmpenv.profile)
    out, err = capsys.readouterr()
    _, name = pod.split("/", 1)
    assert out.strip() == f'pod "{name}" deleted'


def test_watch(tmpenv):
    pod_name = kubectl.get(
        "pod",
        "--output=jsonpath={.items[0].metadata.name}",
        context=tmpenv.profile,
    )
    print("pod_name:", pod_name)
    watcher = kubectl.watch(f"pod/{pod_name}", context=tmpenv.profile)
    for line in watcher:
        pod = json.loads(line)
        print("pod:", pod)
        watcher.close()

    assert pod["metadata"]["name"] == pod_name


def test_watch_leaf(tmpenv):
    pod_name = kubectl.get(
        "pod",
        "--output=jsonpath={.items[0].metadata.name}",
        context=tmpenv.profile,
    )
    print("pod_name:", pod_name)
    watcher = kubectl.watch(
        f"pod/{pod_name}",
        jsonpath="{.metadata.name}",
        context=tmpenv.profile,
    )
    for line in watcher:
        print("line:", line)
        watcher.close()

    assert line == pod_name


def test_watch_jsonpath(tmpenv):
    pod_name = kubectl.get(
        "pod",
        "--output=jsonpath={.items[0].metadata.name}",
        context=tmpenv.profile,
    )
    print("pod_name:", pod_name)
    container_name = kubectl.get(
        f"pod/{pod_name}",
        "--output=jsonpath={.spec.containers[0].name}",
        context=tmpenv.profile,
    )
    print("container_name:", container_name)
    watcher = kubectl.watch(
        f"pod/{pod_name}",
        jsonpath="{.spec.containers[0]}",
        context=tmpenv.profile,
    )
    for line in watcher:
        container = json.loads(line)
        print("container:", container)
        watcher.close()

    assert container["name"] == container_name


def test_watch_events(tmpenv):
    deploy = "deploy/example-deployment"
    label = f"test-{secrets.token_hex(8)}"
    watcher = kubectl.watch(
        deploy,
        jsonpath="{.metadata.labels}",
        timeout=1,
        context=tmpenv.profile,
    )
    with closing(watcher):
        line = next(watcher)
        labels = json.loads(line)
        print("labels:", labels)

        # Add a label and wait for event.
        kubectl.label(deploy, f"{label}=true", context=tmpenv.profile)
        line = next(watcher)
        labels = json.loads(line)
        print("labels:", labels)
        assert labels[label] == "true"

        # Remove the label and wait for event with the label removed.
        kubectl.label(deploy, f"{label}-", context=tmpenv.profile)
        for i in range(3):
            line = next(watcher)
            labels = json.loads(line)
            print("labels:", labels)
            if label not in labels:
                break
        else:
            raise RuntimeError("Timeout waiting for event")


def test_watch_timeout(tmpenv):
    output = []
    with pytest.raises(commands.Timeout):
        watcher = kubectl.watch(
            "deploy/example-deployment",
            jsonpath="{.metadata.name}",
            timeout=0.5,
            context=tmpenv.profile,
        )
        for name in watcher:
            print("line:", name)
            output.append(name)

    # We should get at least the initial state.
    assert output[0] == "example-deployment"


def _get_annotations(resource, context):
    out = kubectl.get(
        resource,
        "--output=jsonpath={.metadata.annotations}",
        context=context,
    )
    if out == "":
        return {}
    return json.loads(out)
