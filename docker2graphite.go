package main

import (
	"path/filepath"
	"fmt"
	"os"
	"path"
	"log"
	"io/ioutil"
	"strings"
	//"strconv"
	"github.com/drags/graphite-golang"
	"time"
	"bytes"
	"flag"
)

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
	prefix := bytes.NewBufferString("test.d2g.")
	prefix.WriteString(filepath.Base(dir))
	now := time.Now().Unix()
	stat_file := path.Join(dir, "memory.stat")
	//log.Print(stat_file)
	lines, err := ioutil.ReadFile(stat_file)
	if err != nil {
		log.Fatal(err)
	}
	metrics := make([]graphite.Metric, len(lines))
	//fmt.Println(string(lines))
	stat_lines := strings.Split(string(lines), "\n")
	for i, st_line := range stat_lines {
		if st_line == "" {
			continue
		}
		kv := strings.Split(st_line, " ")
		metric_name := fmt.Sprintf("%s.%s", prefix.String(), kv[0])
		//metric_value, err := strconv.ParseFloat(kv[1], 64)
		metric_value := kv[1]
		if err != nil {
			log.Print(err)
			continue
		}
		metrics[i] = graphite.NewMetric(metric_name, metric_value, now)
	}
	graphite_client.SendMetrics(metrics)
	done <- 1
}

func main() {
	done := make(chan int)
	graphite_host := flag.String("H", "", "Graphite carbon-cache host, REQUIRED")
	graphite_port := flag.Int("P", 2003, "Graphite carbon-cache plaintext port")
	cgroup_path := flag.String("c", "/sys/fs/cgroup/memory/docker/", "Path to docker in sysfs/cgroup/")
	flag.Parse()

	if *graphite_host == "" {
		log.Fatal("Must provide a graphite carbon-cache host with -H")
	}
	graphite_client := connect_to_graphite(*graphite_host, *graphite_port)

	devices, err := find_containers(*cgroup_path)
	if err != nil {
		log.Fatal("Got err from find_docker_devices:", err)
	}

	for _, path := range devices {
		if path != "" {
			go track_container_dir(graphite_client, path, done)
			<-done
		}
	}
}
