package main

import (
	"flag"
	"os"
	"os/signal"
	"time"

	log "github.com/sirupsen/logrus"
)

func main() {

	log.Fatal()

	var database string
	flag.StringVar(&database, "database", "", "influxdb database name to report metrics to")

	var address string
	flag.StringVar(&address, "address", "", "influxdb address to report metrics to (format: host:port)")

	var interval time.Duration
	flag.DurationVar(&interval, "interval", time.Second*10, "how often to report usage metrics")

	var directories string
	flag.StringVar(&directories, "directories", "", "comma-separated list of directories to scan usage statistics for")

	var dry bool
	flag.BoolVar(&dry, "dry", false, "simulate but do not send metrics")

	var debug bool
	flag.BoolVar(&debug, "debug", false, "enables debug logging")

	flag.Parse()

	if database == "" {
		log.Fatalln("error: database must be defined")
	}

	metricsTicker := time.NewTicker(interval)
	defer metricsTicker.Stop()
	go func() {
		for t := range metricsTicker.C {
			if debug {
				log.Printf("reporting metrics at %s", t.Format(time.RFC3339))
			}
		}
	}()

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt)

	<-shutdownChan

	log.Println("shutting down...")
	os.Exit(0)
}
