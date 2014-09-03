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

//var use_container_names, use_short_id bool
var use_short_id bool

func connect_to_graphite(host string, port int) (*graphite.Graphite) {
	graphite_client, err := graphite.NewGraphite(host, port)
	if err != nil {
		log.Fatal("Failed to connect to graphite", err)
	}
	return graphite_client
}

func find_containers(cgroup_path string) ([]string, error) {
	search_path := strings.TrimRight(cgroup_path, "*/")
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

func track_container_dir(graphite_client *graphite.Graphite, dir string, done chan int) {
	var container_name string
	var metrics []graphite.Metric

	if use_short_id {
		container_name = filepath.Base(dir)[0:12]
	} else {
		container_name = filepath.Base(dir)
	}

	now := time.Now().Unix()

	stat_file := path.Join(dir, "memory.stat")
	lines, err := ioutil.ReadFile(stat_file)
	if err != nil {
		log.Print("Got error when stat'ing memory.stat: ", err)
		// Assume container has disappeared, end goroutine
		done <- 1
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
	done <- 1
}

func main() {
	done := make(chan int)
	graphite_host := flag.String("H", "", "Graphite carbon-cache host, REQUIRED")
	graphite_port := flag.Int("P", 2003, "Graphite carbon-cache plaintext port")
	//graphite_interval := flag.Int("i", 10, "Graphite push interval. A multiple (generally 1) of whisper file resolution")
	graphite_prefix := flag.String("p", "", "Graphite metric prefix: [prefix].<container>.<metric>")
	cgroup_path := flag.String("c", "/sys/fs/cgroup/memory/docker/", "Path to docker in sysfs/cgroup/")
	//flag.BoolVal(&use_container_names, "n", false, "Use container name instead of container ID for metric path")
	flag.BoolVar(&use_short_id, "s", true, "Use 12 character format of container ID for metric path")
	flag.Parse()

	if *graphite_host == "" {
		log.Fatal("Must provide a graphite carbon-cache host with -H")
	}
	graphite_client := connect_to_graphite(*graphite_host, *graphite_port)
	graphite_client.Prefix = *graphite_prefix

	devices, err := find_containers(*cgroup_path)
	if err != nil {
		log.Fatal("Got err from find_containers:", err)
	}

	for _, path := range devices {
		if path != "" {
			go track_container_dir(graphite_client, path, done)
			<-done
		}
	}
}
