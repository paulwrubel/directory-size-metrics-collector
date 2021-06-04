package main

import (
	"flag"
	"io/fs"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	influx "github.com/influxdata/influxdb1-client/v2"
	log "github.com/sirupsen/logrus"
)

func main() {

	log.Infoln("starting program")

	// declaring flags
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

	var logLevel string
	flag.StringVar(&logLevel, "log-level", "info", "specify log level")

	var reportingDepth int
	flag.IntVar(&reportingDepth, "reporting-depth", 0,
		`directory depth to report metrics for. 0 will only report for the given directory list, while 
		1 will report tags for the given directory list and all their immediate subdirectories, and so on.
		The default value is 0.`)

	var tags string
	flag.StringVar(&tags, "tags", "", "optional additional tags for measurements sent to influx")

	// parsing flags
	log.Infoln("parsing flags")
	flag.Parse()
	log.WithFields(log.Fields{
		"database":        database,
		"address":         address,
		"interval":        interval,
		"directories":     directories,
		"dry":             dry,
		"log-level":       logLevel,
		"reporting-depth": reportingDepth,
		"tags":            tags,
	}).Infoln("flags successfully parsed")

	log.Infoln("validating flags")

	// setting log level based on debug flag
	switch logLevel {
	case "panic":
		log.SetLevel(log.PanicLevel)
	case "fatal":
		log.SetLevel(log.FatalLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	case "warn":
		log.SetLevel(log.WarnLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "trace":
		log.SetLevel(log.TraceLevel)
	default:
		log.SetLevel(log.InfoLevel)
	}

	// checking for missing flags
	missingFlags := []string{}
	if database == "" {
		missingFlags = append(missingFlags, "database")
	}
	if address == "" {
		missingFlags = append(missingFlags, "address")
	}
	if directories == "" {
		missingFlags = append(missingFlags, "directories")
	}
	if len(missingFlags) > 0 {
		log.WithField("missing_fields", strings.Join(missingFlags, ",")).Fatalln("missing flags")
	}

	// reformatting directories
	log.Infoln("reformatting directories")
	dirSlice := strings.Split(directories, ",")
	for i, dir := range dirSlice {
		var newDir string = dir
		if strings.Contains(dir, "~") {
			currentUser, err := user.Current()
			if err != nil {
				log.WithError(err).WithField("directory", newDir).Fatalln("error getting current user's to replace '~' with user's home path in directory")
			}
			newDir = strings.ReplaceAll(dir, "~", currentUser.HomeDir)
		}
		newDir, err := filepath.Abs(newDir)
		if err != nil {
			log.WithError(err).WithField("directory", newDir).Fatalln("error getting absolute path for directory")
		}
		dirSlice[i] = newDir
	}

	// expanding directories to desired depth
	log.Infoln("expanding directories to desired depth")
	for i := 0; i < reportingDepth; i++ {
		newDirSlice := []string{}

		for _, dir := range dirSlice {
			subdirs, err := os.ReadDir(dir)
			if err != nil {
				log.WithError(err).WithField("directory", dir).Fatalln("error reading directory for expansion")
			}
			for _, subdir := range subdirs {
				if subdir.IsDir() {
					newDirSlice = append(newDirSlice, filepath.Join(dir, subdir.Name()))
				}
			}
		}

		dirSlice = newDirSlice
	}

	// initialized additional provided tags
	additionalTags := map[string]string{}
	if tags != "" {
		tagsSlice := strings.Split(tags, ",")
		for _, tag := range tagsSlice {
			keyValueTag := strings.Split(tag, "=")
			if len(keyValueTag) != 2 {
				log.Fatalln("tags are malformatted. must be in form \"key=value,another_key=another_value,...\"")
			}
			additionalTags[keyValueTag[0]] = keyValueTag[1]
		}
	}

	// initializing influxdb client
	log.WithField("address", address).Infoln("initializing influxdb client")
	influxClient, err := influx.NewHTTPClient(influx.HTTPConfig{
		Addr: address,
	})
	if err != nil {
		log.WithError(err).Fatalln("error initializing influxdb client")
	}
	defer influxClient.Close()

	// starting metrics ticker
	log.WithField("interval", interval.String()).Infoln("starting metrics ticker")
	metricsTicker := time.NewTicker(interval)
	defer metricsTicker.Stop()
	go func() {
		for t := range metricsTicker.C {
			logEntry := log.WithTime(t)
			dirSizeMap := getAllDirSizesInBytes(logEntry, dirSlice)

			for k, v := range dirSizeMap {
				logEntry.WithFields(log.Fields{
					"directory": k,
					"size":      v,
				}).Debugln("found directory size")
			}

			batchPoints, err := influx.NewBatchPoints(influx.BatchPointsConfig{
				Database: database,
			})
			if err != nil {
				logEntry.WithError(err).Errorln("error creating batch points for influx, skipping...")
				continue
			}

			for k, v := range dirSizeMap {
				point, err := influx.NewPoint("directory_size_in_bytes", mergeTagSets(additionalTags, map[string]string{
					"absolute_path":  k,
					"directory_path": filepath.Dir(k),
					"base_path":      filepath.Base(k),
				}), map[string]interface{}{
					"value": v,
				})
				if err != nil {
					logEntry.WithField("directory", k).WithError(err).Errorln("error creating point for influx, skipping...")
					continue
				}
				logEntry.Debugf("adding point: %s", point.String())

				batchPoints.AddPoint(point)
			}

			if dry {
				logEntry.Debugln("dry run: skipping reporting")
				continue
			}

			err = influxClient.Write(batchPoints)
			if err != nil {
				log.WithError(err).Errorln("error writing points to influx")
			}
		}
	}()

	// establishing shutdown procedure
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt)

	<-shutdownChan

	log.Println("shutting down...")
	os.Exit(0)
}

func getAllDirSizesInBytes(logEntry *log.Entry, directories []string) map[string]int64 {

	log.Traceln("starting directory scan")

	directorySizeMap := map[string]int64{}

	for _, directory := range directories {
		log.WithField("directory", directory).Traceln("starting directory scan")

		directorySize, err := getSingleDirSizeInBytes(logEntry, directory)
		if err != nil {
			log.WithError(err).Errorln("error getting directory size, skipping...")
			continue
		}
		directorySizeMap[directory] = directorySize

		log.WithField("directory", directory).Traceln("finished directory scan")
	}

	log.Traceln("finished directory scan")

	return directorySizeMap
}

func getSingleDirSizeInBytes(logEntry *log.Entry, directory string) (int64, error) {
	var totalBytes int64
	err := filepath.WalkDir(directory, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fileInfo, err := d.Info()
		if err != nil {
			return err
		}
		totalBytes += fileInfo.Size()

		return nil
	})
	if err != nil {
		return 0, err
	}
	return totalBytes, nil
}

func mergeTagSets(tagSets ...map[string]string) map[string]string {
	mergedTags := map[string]string{}
	for _, tagSet := range tagSets {
		for k, v := range tagSet {
			mergedTags[k] = v
		}
	}
	return mergedTags
}
