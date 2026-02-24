# Failure Analysis

10/100 runs failed (10.0%)

## Summary

| Count | Addon | Error Type | Runs |
|------:|-------|------------|------|
| 5 | minio | bucket_creation | 002, 017, 034, 068, 091 |
| 3 | rbd-mirror | timeout | 030, 040, 082 |
| 1 | argocd | dns_failure | 000 |
| 1 | minikube | firewall | 045 |

## Detailed Errors

### Run 000

Log: `000.log`

```
drenv.commands.Error: command failed:
  command:
  - addons/argocd/test
  - hub
  - dr1
  - dr2
  exitcode: 1
  error: |-
    Traceback (most recent call last):
      File "/Users/nsoffer/src/ramen/test/addons/argocd/test", line 122, in <module>
        deploy_busybox(hub, cluster)
        ~~~~~~~~~~~~~~^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/addons/argocd/test", line 30, in deploy_busybox
        for line in commands.watch(
                    ~~~~~~~~~~~~~~^
            "argocd",
            ^^^^^^^^^
        ...<11 lines>...
            env=env,
            ^^^^^^^^
        ):
        ^
      File "/Users/nsoffer/src/ramen/test/drenv/commands.py", line 225, in watch
        raise Error(args, error, exitcode=p.returncode)
drenv.commands.Error: command failed:
      command:
      - argocd
      - app
      - create
      - busybox-dr1
      - --repo=https://github.com/RamenDR/ramen.git
      - --path=test/apps/busybox
      - --dest-name=dr1
      - --dest-namespace=argocd-test
      - --sync-option=CreateNamespace=true
      - --sync-policy=automated
      - --upsert
      exitcode: 20
      error: |-
        time="2026-02-07T00:07:26+02:00" level=error msg="`helm version --client --short` failed exit status 1: Error: unknown flag: --client" execID=8b67f
        time="2026-02-07T00:07:31+02:00" level=fatal msg="rpc error: code = InvalidArgument desc = application spec for busybox-dr1 is invalid: InvalidSpecError: repository not accessible: repositories not accessible: &Repository{Repo: \"https://github.com/RamenDR/ramen.git\", Type: \"\", Name: \"\", Project: \"\"}: repo client error while testing repository: rpc error: code = Unknown desc = error testing repository connectivity: Get \"https://github.com/RamenDR/ramen.git/info/refs?service=git-upload-pack\": dial tcp: lookup github.com on 10.96.0.10:53: server misbehaving"
```

### Run 002

Log: `002.log`

```
drenv.commands.Error: command failed:
  command:
  - addons/minio/start
  - dr1
  exitcode: 1
  error: |-
    Traceback (most recent call last):
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 78, in <module>
        wait(cluster)
        ~~~~^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 67, in wait
        mc.mb(f"{cluster}/{BUCKET}", ignore_existing=True)
        ~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/mc.py", line 29, in mb
        commands.run(*cmd)
        ~~~~~~~~~~~~^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/commands.py", line 124, in run
        raise Error(args, error, exitcode=p.returncode, output=output.decode())
drenv.commands.Error: command failed:
      command:
      - mc
      - mb
      - --ignore-existing
      - dr1/bucket
      exitcode: 1
      error: 'mc: <ERROR> Unable to make bucket `dr1/bucket`. Put "http://192.168.105.12:30000/bucket/":
        dial tcp 192.168.105.12:30000: connect: host is down'
```

### Run 017

Log: `017.log`

```
drenv.commands.Error: command failed:
  command:
  - addons/minio/start
  - dr1
  exitcode: 1
  error: |-
    Traceback (most recent call last):
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 78, in <module>
        wait(cluster)
        ~~~~^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 67, in wait
        mc.mb(f"{cluster}/{BUCKET}", ignore_existing=True)
        ~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/mc.py", line 29, in mb
        commands.run(*cmd)
        ~~~~~~~~~~~~^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/commands.py", line 124, in run
        raise Error(args, error, exitcode=p.returncode, output=output.decode())
drenv.commands.Error: command failed:
      command:
      - mc
      - mb
      - --ignore-existing
      - dr1/bucket
      exitcode: 1
      error: 'mc: <ERROR> Unable to make bucket `dr1/bucket`. Put "http://192.168.105.15:30000/bucket/":
        dial tcp 192.168.105.15:30000: connect: host is down'
```

### Run 030

Log: `030.log`

```
drenv.commands.Error: command failed:
  command:
  - addons/rbd-mirror/test
  - dr1
  - dr2
  exitcode: 1
  error: |-
    Traceback (most recent call last):
      File "/Users/nsoffer/src/ramen/test/addons/rbd-mirror/test", line 146, in <module>
        test_volume_replication(cluster1, cluster2)
        ~~~~~~~~~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/addons/rbd-mirror/test", line 72, in test_volume_replication
        kubectl.wait(
        ~~~~~~~~~~~~^
            f"volumereplication/{VR_NAME}",
            ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      ...<3 lines trimmed>...
        )
        ^
      File "/Users/nsoffer/src/ramen/test/drenv/kubectl.py", line 148, in wait
        _watch("wait", *args, context=context, log=log)
        ~~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/kubectl.py", line 238, in _watch
        for line in commands.watch(*cmd, input=input):
                    ~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/commands.py", line 225, in watch
        raise Error(args, error, exitcode=p.returncode)
drenv.commands.Error: command failed:
      command:
      - kubectl
      - wait
      - --context
      - dr1
      - volumereplication/vr-1m
      - --for=condition=Completed
      - --namespace=rook-ceph
      - --timeout=300s
      exitcode: 1
      error: 'error: timed out waiting for the condition on volumereplications/vr-1m'
```

### Run 034

Log: `034.log`

```
drenv.commands.Error: command failed:
  command:
  - addons/minio/start
  - dr2
  exitcode: 1
  error: |-
    Traceback (most recent call last):
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 78, in <module>
        wait(cluster)
        ~~~~^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 67, in wait
        mc.mb(f"{cluster}/{BUCKET}", ignore_existing=True)
        ~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/mc.py", line 29, in mb
        commands.run(*cmd)
        ~~~~~~~~~~~~^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/commands.py", line 124, in run
        raise Error(args, error, exitcode=p.returncode, output=output.decode())
drenv.commands.Error: command failed:
      command:
      - mc
      - mb
      - --ignore-existing
      - dr2/bucket
      exitcode: 1
      error: 'mc: <ERROR> Unable to make bucket `dr2/bucket`. Put "http://192.168.105.4:30000/bucket/":
        dial tcp 192.168.105.4:30000: i/o timeout'
```

### Run 040

Log: `040.log`

```
drenv.commands.Error: command failed:
  command:
  - addons/rbd-mirror/test
  - dr1
  - dr2
  exitcode: 1
  error: |-
    Traceback (most recent call last):
      File "/Users/nsoffer/src/ramen/test/addons/rbd-mirror/test", line 147, in <module>
        test_volume_replication(cluster2, cluster1)
        ~~~~~~~~~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/addons/rbd-mirror/test", line 72, in test_volume_replication
        kubectl.wait(
        ~~~~~~~~~~~~^
            f"volumereplication/{VR_NAME}",
            ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      ...<3 lines trimmed>...
        )
        ^
      File "/Users/nsoffer/src/ramen/test/drenv/kubectl.py", line 148, in wait
        _watch("wait", *args, context=context, log=log)
        ~~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/kubectl.py", line 238, in _watch
        for line in commands.watch(*cmd, input=input):
                    ~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/commands.py", line 225, in watch
        raise Error(args, error, exitcode=p.returncode)
drenv.commands.Error: command failed:
      command:
      - kubectl
      - wait
      - --context
      - dr2
      - volumereplication/vr-1m
      - --for=condition=Completed
      - --namespace=rook-ceph
      - --timeout=300s
      exitcode: 1
      error: 'error: timed out waiting for the condition on volumereplications/vr-1m'
```

### Run 045

Log: `045.log`

```
drenv.commands.Error: command failed:
  command:
  - minikube
  - start
  - --profile
  - dr2
  - --driver
  - vfkit
  - --container-runtime
  - containerd
  - --extra-disks
  - '1'
  - --disk-size
  - 50g
  - --network
  - vmnet-shared
  - --nodes
  - '1'
  - --cni
  - auto
  - --cpus
  - '4'
  - --memory
  - 8g
  - --extra-config
  - kubelet.serialize-image-pulls=false
  - --alsologtostderr
  - --rosetta
  exitcode: 70
  error: "I0207 03:42:57.098158   56947 out.go:360] Setting OutFile to fd 1 ...\n\
    I0207 03:42:57.098369   56947 out.go:413] isatty.IsTerminal(1) = false\nI0207\
    \ 03:42:57.098374   56947 out.go:374] Setting ErrFile to fd 2...\nI0207 03:42:57.098377\
    \   56947 out.go:413] isatty.IsTerminal(2) = false\nI0207 03:42:57.098472   56947\
    \ root.go:338] Updating PATH: /Users/nsoffer/.minikube/bin\nI0207 03:42:57.099801\
    \   56947 out.go:368] Setting JSON to false\nI0207 03:42:57.140440   56947 start.go:134]\
    \ hostinfo: {\"hostname\":\"MacBookPro\",\"uptime\":189436,\"bootTime\":1770239141,\"\
    procs\":395,\"os\":\"darwin\",\"platform\":\"darwin\",\"platformFamily\":\"Standalone\
    \ Workstation\",\"platformVersion\":\"26.2\",\"kernelVersion\":\"25.2.0\",\"kernelArch\"\
    :\"arm64\",\"virtualizationSystem\":\"\",\"virtualizationRole\":\"\",\"hostId\"\
    ...<31501 lines trimmed>...
    \ \u2502\n\u2502                                                             \
    \                                \u2502\n\u2570\u2500\u2500\u2500\u2500\u2500\u2500\
    \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\
    \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\
    \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\
    \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\
    \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\
    \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\
    \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u256F\nI0207 03:45:08.719005\
    \   56947 out.go:203]"
```

### Run 068

Log: `068.log`

```
drenv.commands.Error: command failed:
  command:
  - addons/minio/start
  - dr2
  exitcode: 1
  error: |-
    Traceback (most recent call last):
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 78, in <module>
        wait(cluster)
        ~~~~^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 67, in wait
        mc.mb(f"{cluster}/{BUCKET}", ignore_existing=True)
        ~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/mc.py", line 29, in mb
        commands.run(*cmd)
        ~~~~~~~~~~~~^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/commands.py", line 124, in run
        raise Error(args, error, exitcode=p.returncode, output=output.decode())
drenv.commands.Error: command failed:
      command:
      - mc
      - mb
      - --ignore-existing
      - dr2/bucket
      exitcode: 1
      error: 'mc: <ERROR> Unable to make bucket `dr2/bucket`. Put "http://192.168.105.39:30000/bucket/":
        dial tcp 192.168.105.39:30000: connect: host is down'
```

### Run 082

Log: `082.log`

```
drenv.commands.Error: command failed:
  command:
  - addons/rbd-mirror/test
  - dr1
  - dr2
  exitcode: 1
  error: |-
    Traceback (most recent call last):
      File "/Users/nsoffer/src/ramen/test/addons/rbd-mirror/test", line 146, in <module>
        test_volume_replication(cluster1, cluster2)
        ~~~~~~~~~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/addons/rbd-mirror/test", line 72, in test_volume_replication
        kubectl.wait(
        ~~~~~~~~~~~~^
            f"volumereplication/{VR_NAME}",
            ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      ...<3 lines trimmed>...
        )
        ^
      File "/Users/nsoffer/src/ramen/test/drenv/kubectl.py", line 148, in wait
        _watch("wait", *args, context=context, log=log)
        ~~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/kubectl.py", line 238, in _watch
        for line in commands.watch(*cmd, input=input):
                    ~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/commands.py", line 225, in watch
        raise Error(args, error, exitcode=p.returncode)
drenv.commands.Error: command failed:
      command:
      - kubectl
      - wait
      - --context
      - dr1
      - volumereplication/vr-1m
      - --for=condition=Completed
      - --namespace=rook-ceph
      - --timeout=300s
      exitcode: 1
      error: 'error: timed out waiting for the condition on volumereplications/vr-1m'
```

### Run 091

Log: `091.log`

```
drenv.commands.Error: command failed:
  command:
  - addons/minio/start
  - dr1
  exitcode: 1
  error: |-
    Traceback (most recent call last):
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 78, in <module>
        wait(cluster)
        ~~~~^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/addons/minio/start", line 67, in wait
        mc.mb(f"{cluster}/{BUCKET}", ignore_existing=True)
        ~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/mc.py", line 29, in mb
        commands.run(*cmd)
        ~~~~~~~~~~~~^^^^^^
      File "/Users/nsoffer/src/ramen/test/drenv/commands.py", line 124, in run
        raise Error(args, error, exitcode=p.returncode, output=output.decode())
drenv.commands.Error: command failed:
      command:
      - mc
      - mb
      - --ignore-existing
      - dr1/bucket
      exitcode: 1
      error: 'mc: <ERROR> Unable to make bucket `dr1/bucket`. Put "http://192.168.105.31:30000/bucket/":
        dial tcp 192.168.105.31:30000: connect: host is down'
```
