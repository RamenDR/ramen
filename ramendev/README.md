# The ramendev tool

The `ramendev` tool deploys and configures *ramen* on your development
clusters.

## Installing

The ramendev tool is installed when creating the python virtual
environment.

To update existing virtual environment run this in the root directory:

```
make venv
```

## Deploying ramen on the hub and managed clusters

Deploy *ramen* from source using `quay.io/ramendr/ramen-operator:latest`
on the hub and managed clusters specified in FILENAME.

```
ramendev deploy FILENAME
```

## Configure ramen hub operator

After deploying *ramen* we need to configure it for the environment. The
configuration depends on the topology specified in the environment file.

```
ramendev config FILENAME
```

## Unconfigure ramen hub operator

Before undeploying *ramen* unconfigure it so undo the changes made by
`ramendev config`.

```
ramendev unconfig FILENAME
```

## Undeploying ramen on the hub and managed clusters

Delete resources deployed by `ramendev deploy` on the hub and managed
clusters.

```
ramendev undeploy FILENAME
```

## Using isolated environments

If we started a `drenv` environment using `--name-prefix` we must use
the same argument when using `ramendev`:

```
drenv start --name-prefix test- envs/regional-dr.yaml
ramendev deploy --name-prefix test- envs/regional-dr.yaml
ramendev config --name-prefix test- envs/regional-dr.yaml
```
