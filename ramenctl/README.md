# The ramenctl tool

The `ramenctl` tool deploys and configures *ramen* on your development
clusters.

## Installing

The ramenctl tool is installed when creating the python virtual
environment.

To update existing virtual environment run this in the root directory:

```
make venv
```

## Deploying ramen on the hub and managed clusters

Deploy *ramen* from source using `quay.io/ramendr/ramen-operator:latest`
on the hub and managed clusters.

```
ramenctl deploy
```

## Configure ramen hub operator

After deploying *ramen* we need to configure it for the environment. The
configuration depends on the environment type (`regional-dr` or
`metro-dr`).

```
ramenctl config regional-dr
```

## Unconfigure ramen hub operator

Before undeploying *ramen* unconfigure it so undo the changes made by
`ramenctl config`.

```
ramenctl unconfig regional-dr
```

## Undeploying ramen on the hub and managed clusters

Delete resources deployed by `ramenctl deploy` on the hub and managed
clusters.

```
ramenctl undeploy
```
