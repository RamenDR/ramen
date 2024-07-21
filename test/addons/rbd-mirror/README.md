# rbd-mirror addon

## Enabling debug logs

If Ceph developers require debug logs you can enable them by starting
the environment with RBD_MIRROR_DEBUG=1:

```
RBD_MIRROR_DEBUG=1 drenv start envs/regional-dr.yaml
```

If the environment is already running you can enable the logs using:

```
RBD_MIRROR_DEBUG=1 addons/rbd-mirror/start dr1 dr2
```
