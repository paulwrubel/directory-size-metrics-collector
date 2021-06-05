package main

import (
	"errors"
	"io/fs"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	influx "github.com/influxdata/influxdb1-client/v2"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func main() {

	log.SetOutput(os.Stdout)

	log.Infoln("starting program")

	if len(os.Args) < 2 {
		log.WithError(errors.New("not enough arguments")).Fatalln("usage: ./collector [CONFIG_FILE].yaml")
	}

	// setting reasonable config defaults
	viper.SetDefault("reporting.interval", 10*time.Second)
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("reporting.depth", 0)
	viper.SetDefault("is_dry", false)

	viper.SetConfigFile(os.Args[1])
	err := viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.WithError(err).Fatalln("specified config file not found")
		} else {
			log.WithError(err).Fatalln("error reading in config file")
		}
	}

	log.Infoln("validating configuration")

	// setting log level based on debug flag
	switch viper.GetString("logging.level") {
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

	log.Debugln("logging detected config below:")
	log.Debugln("--------")
	for key, value := range viper.AllSettings() {
		log.Debugf("%s = %v", key, value)
	}
	log.Debugln("--------")

	// checking for missing keys
	missingKeys := []string{}
	if !viper.IsSet("influx.database") {
		missingKeys = append(missingKeys, "database")
	}
	if !viper.IsSet("influx.address") {
		missingKeys = append(missingKeys, "address")
	}
	if !viper.IsSet("directories") {
		missingKeys = append(missingKeys, "directories")
	}
	if len(missingKeys) > 0 {
		log.WithField("missing_keys", strings.Join(missingKeys, ",")).Fatalln("missing keys in config")
	}

	// trimming directories
	directories := viper.GetStringSlice("directories")
	for i := range directories {
		directories[i] = strings.TrimSpace(directories[i])
		if strings.Contains(directories[i], "~") {
			currentUser, err := user.Current()
			if err != nil {
				log.WithError(err).WithField("directory", directories[i]).Fatalln("error getting current user's to replace '~' with user's home path in directory")
			}
			directories[i] = strings.ReplaceAll(directories[i], "~", currentUser.HomeDir)
		}
		directories[i], err = filepath.Abs(directories[i])
		if err != nil {
			log.WithError(err).WithField("directory", directories[i]).Fatalln("error getting absolute path for directory")
		}
	}

	// expanding directories to desired depth
	log.Infoln("expanding directories to desired depth")
	for i := 0; i < viper.GetInt("reporting.depth"); i++ {
		newDirSlice := []string{}

		for _, dir := range directories {
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

		directories = newDirSlice
	}

	// initializing influxdb client
	address := viper.GetString("influx.address")
	log.WithField("address", address).Infoln("initializing influxdb client")
	influxClient, err := influx.NewHTTPClient(influx.HTTPConfig{
		Addr: address,
	})
	if err != nil {
		log.WithError(err).Fatalln("error initializing influxdb client")
	}
	defer influxClient.Close()

	// starting metrics ticker
	database := viper.GetString("influx.database")
	additionalTags := viper.GetStringMapString("reporting.tags")
	isDry := viper.GetBool("is_dry")
	interval := viper.GetDuration("reporting.interval")

	log.WithField("interval", interval.String()).Infoln("starting metrics ticker")
	metricsTicker := time.NewTicker(interval)
	defer metricsTicker.Stop()
	go func() {
		for t := range metricsTicker.C {
			logEntry := log.WithTime(t)
			dirSizeMap := getAllDirSizesInBytes(logEntry, directories)

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

			if isDry {
				logEntry.Debugln("dry run: skipping reporting")
				continue
			}

			log.Infoln("sending points to influx")
			err = influxClient.Write(batchPoints)
			if err != nil {
				log.WithError(err).Errorln("error writing points to influx")
			}
		}
	}()

	// establishing shutdown procedure
	log.Infoln("waiting for shutdown signal...")
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt)

	<-shutdownChan

	log.Infoln("shutting down...")
	return
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
