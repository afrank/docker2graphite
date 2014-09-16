package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/drags/graphite-golang"
	"gopkg.in/fsnotify.v1"
)

var use_short_id bool
var graphite_interval int
var graphite_client *graphite.Graphite

type ContainerTracker func(path string, container_done chan string)

func connect_to_graphite(host string, port int) {
	var err error
	graphite_client, err = graphite.NewGraphite(host, port)
	if err != nil {
		log.Fatal("Failed to connect to graphite: ", err)
	}
}

func find_containers(sysfs_path string) ([]string, error) {
	search_path := strings.TrimRight(sysfs_path, "*/")
	search_path = fmt.Sprintf("%s/*", search_path)
	possible_containers, _ := filepath.Glob(search_path)

	var container_dirs []string
	for _, path := range possible_containers {
		fi, err := os.Stat(path)
		if err != nil {
			fmt.Println("Got err while stat'ing container directory: ", err)
			continue
		}

		if m := fi.Mode(); m.IsDir() {
			container_dirs = append(container_dirs, path)
		}
	}
	return container_dirs, nil
}

func get_container_name(dir string) (name string) {

	if use_short_id {
		name = filepath.Base(dir)[0:12]
	} else {
		name = filepath.Base(dir)
	}

	return name
}

func getMetricsFromTable(stat_file string, metric_prefix string) (metrics []graphite.Metric, err error) {
	now := time.Now().Unix()

	lines, err := ioutil.ReadFile(stat_file)
	if err != nil {
		return nil, err
	}

	stat_lines := strings.Split(string(lines), "\n")
	for _, st_line := range stat_lines {
		if st_line == "" {
			continue
		}
		kv := strings.Split(st_line, " ")

		metric_name := fmt.Sprintf("%s.%s", metric_prefix, kv[0])
		metric_value := kv[1]

		metrics = append(metrics, graphite.NewMetric(metric_name, metric_value, now))
	}
	return metrics, nil
}

func getMetricsSingleItem(statFilePath string, metric_prefix string) (metrics []graphite.Metric, err error) {
	now := time.Now().Unix()

	lines, err := ioutil.ReadFile(statFilePath)
	if err != nil {
		return nil, err
	}

	metric_name := fmt.Sprintf("%s.%s", metric_prefix, strings.Replace(path.Base(statFilePath), ".", "_", -1))
	metric_value := strings.TrimSpace(string(lines))
	//fmt.Println("Got single item value: ", metric_value, ". In file: ", statFilePath)

	metrics = append(metrics, graphite.NewMetric(metric_name, metric_value, now))
	return metrics, nil
}

func track_container_memory(dir string, container_done chan string) {
	container_name := get_container_name(filepath.Base(dir))
	stat_file := path.Join(dir, "memory.stat")
	metric_prefix := container_name + ".memory"
	var metrics []graphite.Metric
	var err error

	for {
		metrics, err = getMetricsFromTable(stat_file, metric_prefix)
		if err != nil {
			log.Println("Got error when polling memory.stat: ", err)
			// Assume container has disappeared, end goroutine
			container_done <- dir
		}

		graphite_client.SendMetrics(metrics)
		time.Sleep(time.Duration(graphite_interval) * time.Second)
	}
	container_done <- dir
}

func track_container_cpuacct(dir string, container_done chan string) {
	container_name := get_container_name(filepath.Base(dir))
	metric_prefix := container_name + ".cpuacct"
	var metrics []graphite.Metric

	metricsToPoll := make(map[string]func(statFile, metricPrefix string) ([]graphite.Metric, error))
	metricsToPoll["cpuacct.stat"] = getMetricsFromTable
	metricsToPoll["cpuacct.usage"] = getMetricsSingleItem
	//metricsToPoll["cpuacct.usage_percpu"] = getMetricsSingleLineArray

	for {
		for statFile, metricFunc := range metricsToPoll {
			statFilePath := path.Join(dir, statFile)
			polledMetrics, err := metricFunc(statFilePath, metric_prefix)
			if err != nil {
				log.Println("Got error fetching stats from file: ", statFile, " : ", err)
			}
			metrics = append(metrics, polledMetrics...)
		}

		//cpuacct_usage_metrics, err = getMetricsFromSingle(usage_file, metric_prefix)
		//if err != nil {
		//	log.Println("Got error when polling cpuacct.stat: ", err)
		//	// Assume container has disappeared, end goroutine
		//	container_done <- dir
		//}
		//metrics = append(metrics, cpuacct_stat_metrics)
		//
		//cpuacct_usagepercpu_metrics, err = getMetricsSingleArray(usagepercpu_file, metric_prefix)
		//if err != nil {
		//	log.Println("Got error when polling cpuacct.stat: ", err)
		//	// Assume container has disappeared, end goroutine
		//	container_done <- dir
		//}
		//metrics = append(metrics, cpuacct_stat_metrics)

		graphite_client.SendMetrics(metrics)
		time.Sleep(time.Duration(graphite_interval) * time.Second)
		metrics = nil
	}
	container_done <- dir
}
func watch_sysfs_dir(sysfs_path string, track_func ContainerTracker, wd chan bool) {
	container_done := make(chan string)
	watched_containers := make(map[string]bool)

	// closure to handle accounting at goroutine start
	start_container_dir := func(path string) {
		if path != "" && watched_containers[path] == false {
			log.Println("Adding new container with path: ", path)
			watched_containers[path] = true
			go track_func(path, container_done)
		}
	}

	// Find and start existing containers
	// TODO ensure path exists
	containers, err := find_containers(sysfs_path)
	if err != nil {
		log.Fatal("Got err from find_containers: ", err)
	}
	for _, path := range containers {
		start_container_dir(path)
	}

	// Watch directory for new containers
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal("Got error from creating fsnotify watcher: ", err)
	}
	defer watcher.Close()
	// watch sysfs path
	err = watcher.Add(sysfs_path)
	if err != nil {
		log.Fatal("Got error from adding path ", sysfs_path, " to watcher: ", err)
	}

	for {
		select {
		// Handle fsnotify create events.
		case event := <-watcher.Events:
			//log.Println("event: ", event)
			if event.Op&fsnotify.Create == fsnotify.Create {
				//log.Println("Saw created file: ", event.Name)
				// If file named in create event is directory, start tracking
				fi, err := os.Stat(event.Name)
				if err != nil {
					fmt.Println("Got error from os.Stat on event.Name: ", err)
					break
				}
				if m := fi.Mode(); m.IsDir() {
					start_container_dir(event.Name)
				}
			}
		// Handle done signals from track_container_dir
		case done_container := <-container_done:
			log.Println("Removing finished container with path: ", done_container)
			watched_containers[done_container] = false
		}
	}
	wd <- true
}

func main() {
	graphite_host := flag.String("H", "", "Graphite carbon-cache host, REQUIRED")
	graphite_port := flag.Int("P", 2003, "Graphite carbon-cache plaintext port")
	graphite_prefix := flag.String("p", "", "Graphite metric prefix: [prefix].<container>.<metric>")
	flag.IntVar(&graphite_interval, "i", 10, "Graphite push interval. A multiple (generally 1) of whisper file resolution")
	sysfs_path := flag.String("c", "/sys/fs/cgroup/", "Path cgroup in sysfs")
	flag.BoolVar(&use_short_id, "s", true, "Use 12 character format of container ID for metric path")
	flag.Parse()

	if *graphite_host == "" {
		log.Fatal("Must provide a graphite carbon-cache host with -H")
	}
	connect_to_graphite(*graphite_host, *graphite_port)
	graphite_client.Prefix = *graphite_prefix

	memory_path := *sysfs_path + "memory/docker"
	cpuacct_path := *sysfs_path + "cpuacct/docker"
	//bklio_path := *sysfs_path + "blkio/docker"

	watcher_done := make(chan bool)
	go watch_sysfs_dir(memory_path, track_container_memory, watcher_done)
	go watch_sysfs_dir(cpuacct_path, track_container_cpuacct, watcher_done)
	<-watcher_done
	<-watcher_done
}
