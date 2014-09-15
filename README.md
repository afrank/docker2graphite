# docker2graphite

## About

A quick utility to pump docker container metrics into graphite. This 0.1 version only pushes the memory.stat metrics. More to come shortly.

## Usage

	Usage of docker2graphite:
	  -H="": Graphite carbon-cache host, REQUIRED
	  -P=2003: Graphite carbon-cache plaintext port
	  -c="/sys/fs/cgroup/memory/docker/": Path to docker in sysfs/cgroup/memory
	  -i=10: Graphite push interval. A multiple (generally 1x) of whisper file resolution
	  -p="": Graphite metric prefix: [prefix].<container>.<metric>
	  -s=true: Use 12 character format of container ID for metric path

#### Options

- **-H:** Hostname of the destination graphite carbon-cache instance
- **-P:** Port of destination carbon-cache instance. Defaults to 2003
- **-c:** Path to docker directory within cgroup stats memory tree. Defaults to `/sys/fs/cgroup/memory/docker`. If sysfs has been mounted elsewhere, use `find <sysfs mount point> -wholename '*cgroup/memory/docker'` to locate the proper value.
- **-i:** Interval between metrics collection. Should be a multiple (generally 1x) of the lowest whisper file resolution. Refer to the [Whisper database documentation](http://graphite.readthedocs.org/en/latest/whisper.html) for more information.
- **-p:** Prefix for metrics. This string will be prepended to the <container>.<metric> string to generate the full name of the metric. See the [Plan a Naming Hierarchy](http://graphite.readthedocs.org/en/latest/feeding-carbon.html#step-1-plan-a-naming-hierarchy) section of the Graphite documentation for more information.
- **-s:** Whether to use shorted (12-character) container IDs in the metric name. Defaults to true.

## Disclaimer

This software is in extremely early development and represents the author's first public Go project. Please report any issues with performance, composition, or documentation to the [Issues page](https://github.com/drags/docker2graphite/issues).
