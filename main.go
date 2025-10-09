// SPDX-License-Identifier: Apache-2.0
// (c) Kubernetes authors + your additions for /burn

package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	negroni "github.com/urfave/negroni/v3"
	simpleredis "github.com/xyproto/simpleredis/v2"

	"github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/collectors"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	masterPool *simpleredis.ConnectionPool

	    redisOps   = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "dojo_redis_operations_total",
            Help: "Count of Redis operations by type",
        },
        []string{"op"},
    )
)

// ---------- helpers ----------

func atoiDefault(s string, d int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return d
}

func HandleError(result interface{}, err error) (r interface{}) {
	if err != nil {
		panic(err)
	}
	return result
}

// ---------- handlers ----------

func ListRangeHandler(rw http.ResponseWriter, req *http.Request) {
	key := mux.Vars(req)["key"]
	list := simpleredis.NewList(masterPool, key)
	members := HandleError(list.GetAll()).([]string)
	membersJSON := HandleError(json.MarshalIndent(members, "", "  ")).([]byte)
	rw.Header().Set("Content-Type", "application/json")
	rw.Write(membersJSON)
}

func ListPushHandler(rw http.ResponseWriter, req *http.Request) {
	key := mux.Vars(req)["key"]
	value := mux.Vars(req)["value"]
	list := simpleredis.NewList(masterPool, key)
	HandleError(nil, list.Add(value))
	ListRangeHandler(rw, req)
}

func InfoHandler(rw http.ResponseWriter, req *http.Request) {
	info := HandleError(masterPool.Get(0).Do("INFO")).([]byte)
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.Write(info)
}

func EnvHandler(rw http.ResponseWriter, req *http.Request) {
	environment := make(map[string]string)
	for _, item := range os.Environ() {
		splits := strings.Split(item, "=")
		key := splits[0]
		val := strings.Join(splits[1:], "=")
		environment[key] = val
	}
	envJSON := HandleError(json.MarshalIndent(environment, "", "  ")).([]byte)
	rw.Header().Set("Content-Type", "application/json")
	rw.Write(envJSON)
}

func HealthHandler(rw http.ResponseWriter, req *http.Request) {
	if err := masterPool.Ping(); err != nil {
		rw.WriteHeader(http.StatusInternalServerError)
		rw.Write([]byte(err.Error()))
		return
	}
	rw.WriteHeader(http.StatusOK)
}

// /burn?seconds=20&workers=<cpus>&mem_mb=0
// Generates CPU load with optional memory allocation for HPA testing.
// - seconds: how long to burn CPU (default 20)
// - workers: number of goroutines (default NumCPU)
// - mem_mb: allocate this many MB and touch pages (default 0)
func BurnHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	seconds := atoiDefault(q.Get("seconds"), 20)
	if seconds < 1 {
		seconds = 1
	}
	workers := atoiDefault(q.Get("workers"), runtime.NumCPU())
	if workers < 1 {
		workers = 1
	}
	memMB := atoiDefault(q.Get("mem_mb"), 0)
	if memMB < 0 {
		memMB = 0
	}

	// optional memory pressure: allocate and touch mem_mb MiB
	var junk [][]byte
	for i := 0; i < memMB; i++ {
		b := make([]byte, 1024*1024)
		for j := 0; j < len(b); j += 4096 {
			b[j] = byte(j)
		}
		junk = append(junk, b)
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(seconds)*time.Second)
	defer cancel()

	done := make(chan struct{})
	// spin up CPU workers
	for i := 0; i < workers; i++ {
		go func() {
			// tight loop with math so compiler can't optimize away
			x := 0.0001
			for {
				select {
				case <-ctx.Done():
					done <- struct{}{}
					return
				default:
					x += math.Sqrt(x)
					if x > 1e9 {
						x = 0.0001
					}
				}
			}
		}()
	}

	// wait for all workers to finish or context timeout
	for i := 0; i < workers; i++ {
		<-done
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ---------- main ----------

func main() {

	    prometheus.MustRegister(
        collectors.NewGoCollector(),
        collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
        redisOps,
    )

	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost"
	}
	masterPool = simpleredis.NewConnectionPoolHost(redisHost + ":6379")
	defer masterPool.Close()

	r := mux.NewRouter()
	r.Path("/lrange/{key}").Methods("GET").HandlerFunc(ListRangeHandler)
	r.Path("/rpush/{key}/{value}").Methods("GET").HandlerFunc(ListPushHandler)
	r.Path("/info").Methods("GET").HandlerFunc(InfoHandler)
	r.Path("/env").Methods("GET").HandlerFunc(EnvHandler)
	r.Path("/healthz").Methods("GET").HandlerFunc(HealthHandler)
	r.Path("/burn").Methods("GET").HandlerFunc(BurnHandler)
	r.Path("/metrics").Methods("GET").Handler(promhttp.Handler())


	n := negroni.Classic()
	n.UseHandler(r)
	n.Run(":3000")
}
