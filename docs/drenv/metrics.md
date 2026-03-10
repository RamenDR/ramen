<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# drenv metrics

The `drenv stress-test report` command automatically collects system metrics
from Netdata if it is running. This is important to correlate test failures to
system load.

## Installing Netdata

macOS:

```
brew install netdata
```

Linux:

```
curl -fsSL https://get.netdata.cloud/kickstart.sh -o /tmp/netdata-kickstart.sh
bash /tmp/netdata-kickstart.sh --no-updates --stable-channel
```

## Configuring netdata

For stress testing, configure Netdata to use minimal resources and store
data on disk (persists across restarts, uses less memory).

Edit `/opt/homebrew/etc/netdata/netdata.conf` (macOS) or
`/etc/netdata/netdata.conf` (Linux):

```
[cloud]
enable = no

[global]
update every = 15
memory mode = dbengine

[db]
mode = dbengine
dbengine multihost disk space MB = 1024
```

This configuration:

- Collects metrics every 15 seconds (good balance of detail vs overhead)
- Stores data on disk (up to 1 GB, keeps weeks of history)
- Uses minimal memory (~64 MB for cache)
- Safe to leave running permanently

## Start netdata service

Start Netdata before running a stress test:

macOS:

```
brew services start netdata
```

Linux:

```
sudo systemctl start netdata
```

Verify Netdata is running by opening
[http://localhost:19999](http://localhost:19999) in a browser.

## Stop netdata service

Stop Netdata after collecting metrics:

macOS:

```
brew services stop netdata
```

Linux:

```
sudo systemctl stop netdata
```

## Remote access (Linux)

By default Netdata listens on localhost only. To access it from another
machine, configure it to listen on all interfaces.

Edit `/etc/netdata/netdata.conf`:

```
[web]
bind to = 0.0.0.0
```

Restart Netdata and open the firewall port:

```
sudo systemctl restart netdata
sudo firewall-cmd --zone=public --add-port=19999/tcp --permanent
sudo firewall-cmd --reload
```

Then access Netdata at `http://<host>:19999`.
