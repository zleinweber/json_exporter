// Copyright 2020 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus-community/json_exporter/config"
	"github.com/prometheus-community/json_exporter/exporter"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/common/promlog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	configFile    = kingpin.Flag("config.file", "JSON exporter configuration file.").Default("config.yml").ExistingFile()
	listenAddress = kingpin.Flag("web.listen-address", "The address to listen on for HTTP requests.").Default(":7979").String()
	configCheck   = kingpin.Flag("config.check", "If true validate the config file and then exit.").Default("false").Bool()
	tlsConfigFile = kingpin.Flag("web.config", "[EXPERIMENTAL] Path to config yaml file that can enable TLS or authentication.").Default("").String()
)

func Run() {

	promlogConfig := &promlog.Config{}

	flag.AddFlags(kingpin.CommandLine, promlogConfig)
	kingpin.Version(version.Print("json_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	logger := promlog.New(promlogConfig)

	level.Info(logger).Log("msg", "Starting json_exporter", "version", version.Info())
	level.Info(logger).Log("msg", "Build context", "build", version.BuildContext())

	level.Info(logger).Log("msg", "Loading config file", "file", *configFile)
	config, err := config.LoadConfig(*configFile)
	if err != nil {
		level.Error(logger).Log("msg", "Error loading config", "err", err)
		os.Exit(1)
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		level.Error(logger).Log("msg", "Failed to marshal config to JSON", "err", err)
	}
	level.Info(logger).Log("msg", "Loaded config file", "config", string(configJSON))

	if *configCheck {
		os.Exit(0)
	}

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/probe", func(w http.ResponseWriter, req *http.Request) {
		probeHandler(w, req, logger, config)
	})

	server := &http.Server{Addr: *listenAddress}
	if err := web.ListenAndServe(server, *tlsConfigFile, logger); err != nil {
		level.Error(logger).Log("msg", "Failed to start the server", "err", err)
		os.Exit(1)
	}
}

func probeHandler(w http.ResponseWriter, r *http.Request, logger log.Logger, config config.Config) {

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)

	registry := prometheus.NewPedanticRegistry()

	metrics, err := exporter.CreateMetricsList(config)
	if err != nil {
		level.Error(logger).Log("msg", "Failed to create metrics list from config", "err", err)
	}

	jsonMetricCollector := exporter.JSONMetricCollector{JSONMetrics: metrics}
	jsonMetricCollector.Logger = logger

	target := r.URL.Query().Get("target")
	if target == "" {
		http.Error(w, "Target parameter is missing", http.StatusBadRequest)
		return
	}

	fetcher := exporter.NewJSONFetcher(ctx, logger, config, r.URL.Query())
	data, err := fetcher.FetchJSON(target)
	if err != nil {
		http.Error(w, "Failed to fetch JSON response. TARGET: "+target+", ERROR: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	jsonMetricCollector.Data = data

	registry.MustRegister(jsonMetricCollector)
	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)

}
