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
on the hub and managed clusters specified in FILENAME.

```
ramenctl deploy FILENAME
```

## Configure ramen hub operator

After deploying *ramen* we need to configure it for the environment. The
configuration depends on the topology specified in the environment file.

```
ramenctl config FILENAME
```

## Unconfigure ramen hub operator

Before undeploying *ramen* unconfigure it so undo the changes made by
`ramenctl config`.

```
ramenctl unconfig FILENAME
```

## Undeploying ramen on the hub and managed clusters

Delete resources deployed by `ramenctl deploy` on the hub and managed
clusters.

```
ramenctl undeploy FILENAME
```

## Using isolated environments

If we started a `drenv` environment using `--name-prefix` we must use
the same argument when using `ramenctl`:

```
drenv start --name-prefix test- envs/regional-dr.yaml
ramenctl deploy --name-prefix test- envs/regional-dr.yaml
ramenctl config --name-prefix test- envs/regional-dr.yaml
```
