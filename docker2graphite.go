package main

import (
	"path/filepath"
	"fmt"
	"os"
	"path"
	"log"
	"io/ioutil"
	"strings"
	"github.com/drags/graphite-golang"
	"time"
	"flag"
)

var use_short_id bool
var graphite_interval int
var interval_sysfs_watch = 60

func connect_to_graphite(host string, port int) (*graphite.Graphite) {
	graphite_client, err := graphite.NewGraphite(host, port)
	if err != nil {
		log.Fatal("Failed to connect to graphite", err)
	}
	return graphite_client
}

func find_containers(sysfs_path string) ([]string, error) {
	search_path := strings.TrimRight(sysfs_path, "*/")
	search_path = fmt.Sprintf("%s/*", search_path)
	possible_containers, _ := filepath.Glob(search_path)

	var container_dirs []string
	for _, path := range possible_containers {
		fi, err := os.Stat(path)
		if err != nil {
			fmt.Println(err)
			continue
		}

		if m := fi.Mode(); m.IsDir() {
			container_dirs = append(container_dirs, path)
		}
	}
	return container_dirs, nil
}

func track_container_dir(graphite_client *graphite.Graphite, dir string, container_done chan string) {
	var container_name string

	if use_short_id {
		container_name = filepath.Base(dir)[0:12]
	} else {
		container_name = filepath.Base(dir)
	}

	for ;; {
		var metrics []graphite.Metric
		now := time.Now().Unix()

		stat_file := path.Join(dir, "memory.stat")
		lines, err := ioutil.ReadFile(stat_file)
		if err != nil {
			log.Print("Got error when stat'ing memory.stat: ", err)
			// Assume container has disappeared, end goroutine
			container_done <- dir
			return
		}

		stat_lines := strings.Split(string(lines), "\n")
		for _, st_line := range stat_lines {
			if st_line == "" {
				continue
			}
			kv := strings.Split(st_line, " ")

			metric_name := fmt.Sprintf("%s.%s", container_name, kv[0])
			metric_value := kv[1]

			metrics = append(metrics, graphite.NewMetric(metric_name, metric_value, now))
		}
		graphite_client.SendMetrics(metrics)
		time.Sleep(time.Duration(graphite_interval) * time.Second)
		metrics = nil
	}
	container_done <- dir
}

func watch_sysfs_dir(sysfs_path string, graphite_client *graphite.Graphite) {
	container_done := make(chan string)
	watched_containers := make(map[string]bool)

	for ;; {
		log.Print("Starting container check")
		containers, err := find_containers(sysfs_path)
		if err != nil {
			log.Fatal("Got err from find_containers:", err)
		}

		// Check for and run any new containers
		for _, path := range containers {
			if path != "" && watched_containers[path] == false {
				log.Print("Adding new container with path: ", path)
				watched_containers[path] = true
				go track_container_dir(graphite_client, path, container_done)
			}
		}

		select {
		case done_container := <-container_done:
			if ! watched_containers[done_container] {
				log.Fatal("Got done notification for untracked container: ", done_container)
			}
			log.Print("Removing finished container with path: ", done_container)
			watched_containers[done_container] = false
		default:
			break
		}

		time.Sleep(time.Duration(interval_sysfs_watch) * time.Second)
	}
}

func main() {
	graphite_host := flag.String("H", "", "Graphite carbon-cache host, REQUIRED")
	graphite_port := flag.Int("P", 2003, "Graphite carbon-cache plaintext port")
	graphite_prefix := flag.String("p", "", "Graphite metric prefix: [prefix].<container>.<metric>")
	flag.IntVar(&graphite_interval, "i", 10, "Graphite push interval. A multiple (generally 1) of whisper file resolution")
	sysfs_path := flag.String("c", "/sys/fs/cgroup/memory/docker/", "Path to docker in sysfs/cgroup/memory")
	flag.BoolVar(&use_short_id, "s", true, "Use 12 character format of container ID for metric path")
	flag.Parse()

	if *graphite_host == "" {
		log.Fatal("Must provide a graphite carbon-cache host with -H")
	}
	graphite_client := connect_to_graphite(*graphite_host, *graphite_port)
	graphite_client.Prefix = *graphite_prefix

	watch_sysfs_dir(*sysfs_path, graphite_client)
}
