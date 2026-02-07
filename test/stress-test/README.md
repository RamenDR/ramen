# drenv stress test

This directory includes the drenv stress test for evaluating `drenv start`
robustness and debugging failed runs.

The test support 2 modes of operation:

- Collecting stats from long unattended test run
- Debugging a failed run

## Collecting stats

In this example we run 100 runs starting the regional-dr environment. If
a run fails, we delete the clusters and continue. This is useful for
understanding what are the most common failures.

```
stress-test/run -r 100 ../envs/regional-dr.yaml
```

This creates the `out` directory in the current directory, logging each
run in a separate log file, and saving test results in `test.json` file.
This run took more than 17 hours (626 seconds per build):

```
$ ls out
000.log  013.log  026.log  039.log  052.log  065.log  078.log  091.log
001.log  014.log  027.log  040.log  053.log  066.log  079.log  092.log
002.log  015.log  028.log  041.log  054.log  067.log  080.log  093.log
003.log  016.log  029.log  042.log  055.log  068.log  081.log  094.log
004.log  017.log  030.log  043.log  056.log  069.log  082.log  095.log
005.log  018.log  031.log  044.log  057.log  070.log  083.log  096.log
006.log  019.log  032.log  045.log  058.log  071.log  084.log  097.log
007.log  020.log  033.log  046.log  059.log  072.log  085.log  098.log
008.log  021.log  034.log  047.log  060.log  073.log  086.log  099.log
009.log  022.log  035.log  048.log  061.log  074.log  087.log  test.json
010.log  023.log  036.log  049.log  062.log  075.log  088.log
011.log  024.log  037.log  050.log  063.log  076.log  089.log
012.log  025.log  038.log  051.log  064.log  077.log  090.log
```

To get test stats:

```
$ cat out/test.json | jq .stats
{
  "runs": 100,
  "passed": 84,
  "failed": 16,
  "success": 84.0,
  "time": 62694.784591522985,
  "passed-time": 52647.00043984903,
  "failed-time": 9723.622515744006
}
```

To find the failed runs you can use look up the individual tests
results:

```
$ cat out/test.json
...
    {
      "name": "007",
      "passed": false,
      "time": 460.2368865620083
    },
```

Or grep the logs:

```
$ grep ^drenv.commands.Error out/*.log
out/007.log:drenv.commands.Error: Command failed:
out/014.log:drenv.commands.Error: Command failed:
out/026.log:drenv.commands.Error: Command failed:
out/027.log:drenv.commands.Error: Command failed:
out/028.log:drenv.commands.Error: Command failed:
out/031.log:drenv.commands.Error: Command failed:
out/036.log:drenv.commands.Error: Command failed:
out/043.log:drenv.commands.Error: Command failed:
out/044.log:drenv.commands.Error: Command failed:
out/051.log:drenv.commands.Error: Command failed:
out/052.log:drenv.commands.Error: Command failed:
out/066.log:drenv.commands.Error: Command failed:
out/074.log:drenv.commands.Error: Command failed:
out/075.log:drenv.commands.Error: Command failed:
out/085.log:drenv.commands.Error: Command failed:
out/089.log:drenv.commands.Error: Command failed:
```

## Debugging a failed run

In this mode the run exit cleanly after the first failure, leaving the
cluster running for inspection.

```
stress-test/run -r 100 -x ../envs/regional-dr.yaml
```

Because the failures are random, a run may fail very quickly or only
after many hours. As drenv becomes more reliable debugging random
failures will become harder.

> [!IMPORTANT]
> After debugging the failure, you need to delete the environment
> manually.

In this example the run failed after the first run:

```
$ ls out.3:
out.3:
000.log  test.json
```

And here after 20 runs:

```
$ ls out.4:
000.log  003.log  006.log  009.log  012.log  015.log  018.log  021.log
001.log  004.log  007.log  010.log  013.log  016.log  019.log  test.json
002.log  005.log  008.log  011.log  014.log  017.log  020.log
```

In both case the last run is the failure:

```
$ grep ^drenv.commands.Error out.[34]/*.log
out.3/000.log:drenv.commands.Error: Command failed:
out.4/021.log:drenv.commands.Error: Command failed:
```

The clusters are running, hopefully in the same state when the run
failed. Sometimes the cluster fixed itself after the failure, this
usually means some timeout was too short.

## Analyzing failures

After a test run, use `analyze-failures` to generate a structured
report of all failures:

```
stress-test/analyze-failures out
```

This creates `failures.md` in the output directory with:

- **Summary**: Error counts by addon
- **Detailed errors**: Full error messages with log file references

The structured format makes it easy to identify the most common failure
patterns and prioritize fixes.
