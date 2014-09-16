package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/drags/graphite-golang"
	"gopkg.in/fsnotify.v1"
)

var useShortID bool
var graphiteInterval int
var graphiteClient *graphite.Graphite

type ContainerTracker func(path string, containerDone chan string)

func connectToGraphite(host string, port int) {
	var err error
	graphiteClient, err = graphite.NewGraphite(host, port)
	if err != nil {
		log.Fatal("Failed to connect to graphite: ", err)
	}
}

func findContainers(sysfsPath string) ([]string, error) {
	searchPath := strings.TrimRight(sysfsPath, "*/")
	searchPath = fmt.Sprintf("%s/*", searchPath)
	possibleContainers, _ := filepath.Glob(searchPath)

	var containerDirs []string
	for _, path := range possibleContainers {
		fi, err := os.Stat(path)
		if err != nil {
			fmt.Println("Got err while stat'ing container directory: ", err)
			continue
		}

		if m := fi.Mode(); m.IsDir() {
			containerDirs = append(containerDirs, path)
		}
	}
	return containerDirs, nil
}

func getContainerName(dir string) (name string) {

	if useShortID {
		name = filepath.Base(dir)[0:12]
	} else {
		name = filepath.Base(dir)
	}

	return name
}

func getMetricsFromTable(statFile string, metricPrefix string) (metrics []graphite.Metric, err error) {
	now := time.Now().Unix()

	lines, err := ioutil.ReadFile(statFile)
	if err != nil {
		return nil, err
	}

	statLines := strings.Split(string(lines), "\n")
	for _, stLine := range statLines {
		if stLine == "" {
			continue
		}
		kv := strings.Split(stLine, " ")

		metricName := fmt.Sprintf("%s.%s", metricPrefix, kv[0])
		metricValue := kv[1]
		metrics = append(metrics, graphite.NewMetric(metricName, metricValue, now))
	}
	return metrics, nil
}

func getMetricsArray(statFilePath string, metricPrefix string) (metrics []graphite.Metric, err error) {
	now := time.Now().Unix()

	lines, err := ioutil.ReadFile(statFilePath)
	if err != nil {
		return nil, err
	}

	// Build prefix up to end of filename
	metricPrefix = fmt.Sprintf("%s.%s", metricPrefix, strings.Replace(path.Base(statFilePath), ".", "_", -1))

	statVals := strings.Split(strings.TrimSpace(string(lines)), " ")
	for index, value := range statVals {
		metricName := fmt.Sprintf("%s.%s", metricPrefix, strconv.Itoa(index))
		metricValue := value
		metrics = append(metrics, graphite.NewMetric(metricName, metricValue, now))
	}
	return metrics, nil
}

func getMetricsSingleItem(statFilePath string, metricPrefix string) (metrics []graphite.Metric, err error) {
	now := time.Now().Unix()

	lines, err := ioutil.ReadFile(statFilePath)
	if err != nil {
		return nil, err
	}

	metricName := fmt.Sprintf("%s.%s", metricPrefix, strings.Replace(path.Base(statFilePath), ".", "_", -1))
	metricValue := strings.TrimSpace(string(lines))
	//fmt.Println("Got single item value: ", metricValue, ". In file: ", statFilePath)

	metrics = append(metrics, graphite.NewMetric(metricName, metricValue, now))
	return metrics, nil
}

func trackContainerMemory(dir string, containerDone chan string) {
	containerName := getContainerName(filepath.Base(dir))
	statFile := path.Join(dir, "memory.stat")
	metricPrefix := containerName + ".memory"
	var metrics []graphite.Metric
	var err error

	for {
		metrics, err = getMetricsFromTable(statFile, metricPrefix)
		if err != nil {
			log.Println("Got error when polling memory.stat: ", err)
			// Assume container has disappeared, end goroutine
			containerDone <- dir
		}

		graphiteClient.SendMetrics(metrics)
		time.Sleep(time.Duration(graphiteInterval) * time.Second)
	}
	containerDone <- dir
}

func trackContainerCpuacct(dir string, containerDone chan string) {
	containerName := getContainerName(filepath.Base(dir))
	metricPrefix := containerName + ".cpuacct"
	var metrics []graphite.Metric

	metricsToPoll := make(map[string]func(statFile, metricPrefix string) ([]graphite.Metric, error))
	metricsToPoll["cpuacct.stat"] = getMetricsFromTable
	metricsToPoll["cpuacct.usage"] = getMetricsSingleItem
	metricsToPoll["cpuacct.usage_percpu"] = getMetricsArray

	for {
		for statFile, metricFunc := range metricsToPoll {
			statFilePath := path.Join(dir, statFile)
			polledMetrics, err := metricFunc(statFilePath, metricPrefix)
			if err != nil {
				log.Println("Got error fetching stats from file: ", statFile, " : ", err)
			}
			metrics = append(metrics, polledMetrics...)
		}
		graphiteClient.SendMetrics(metrics)
		time.Sleep(time.Duration(graphiteInterval) * time.Second)
		metrics = nil
	}
	containerDone <- dir
}

func watchSysfsDir(sysfsPath string, trackFunc ContainerTracker, wd chan bool) {
	containerDone := make(chan string)
	watchedContainers := make(map[string]bool)

	// closure to handle accounting at goroutine start
	startContainerDir := func(path string) {
		if path != "" && watchedContainers[path] == false {
			log.Println("Adding new container with path: ", path)
			watchedContainers[path] = true
			go trackFunc(path, containerDone)
		}
	}

	// Find and start existing containers
	// TODO ensure path exists
	containers, err := findContainers(sysfsPath)
	if err != nil {
		log.Fatal("Got err from findContainers: ", err)
	}
	for _, path := range containers {
		startContainerDir(path)
	}

	// Watch directory for new containers
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal("Got error from creating fsnotify watcher: ", err)
	}
	defer watcher.Close()
	// watch sysfs path
	err = watcher.Add(sysfsPath)
	if err != nil {
		log.Fatal("Got error from adding path ", sysfsPath, " to watcher: ", err)
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
					startContainerDir(event.Name)
				}
			}
		// Handle done signals from trackContainerDir
		case doneContainer := <-containerDone:
			log.Println("Removing finished container with path: ", doneContainer)
			watchedContainers[doneContainer] = false
		}
	}
	wd <- true
}

func main() {
	graphiteHost := flag.String("H", "", "Graphite carbon-cache host, REQUIRED")
	graphitePort := flag.Int("P", 2003, "Graphite carbon-cache plaintext port")
	graphitePrefix := flag.String("p", "", "Graphite metric prefix: [prefix].<container>.<metric>")
	flag.IntVar(&graphiteInterval, "i", 10, "Graphite push interval. A multiple (generally 1) of whisper file resolution")
	sysfsPath := flag.String("c", "/sys/fs/cgroup/", "Path cgroup in sysfs")
	flag.BoolVar(&useShortID, "s", true, "Use 12 character format of container ID for metric path")
	flag.Parse()

	if *graphiteHost == "" {
		log.Fatal("Must provide a graphite carbon-cache host with -H")
	}
	connectToGraphite(*graphiteHost, *graphitePort)
	graphiteClient.Prefix = *graphitePrefix

	memoryPath := *sysfsPath + "memory/docker"
	cpuacctPath := *sysfsPath + "cpuacct/docker"
	//bklioPath := *sysfsPath + "blkio/docker"

	watcherDone := make(chan bool)
	go watchSysfsDir(memoryPath, trackContainerMemory, watcherDone)
	go watchSysfsDir(cpuacctPath, trackContainerCpuacct, watcherDone)
	<-watcherDone
	<-watcherDone
}
