package main

import (
	"errors"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/davecgh/go-spew/spew"
	influx "github.com/influxdata/influxdb1-client/v2"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type Set struct {
	Name              string             `mapstructure:"name"`
	DirectoryMappings []DirectoryMapping `mapstructure:"directories"`
	Depth             int                `mapstructure:"depth"`
}

type DirectoryMapping struct {
	External string `mapstructure:"external"`
	Internal string `mapstructure:"internal"`
}

func main() {

	log.SetOutput(os.Stdout)

	log.Infoln("starting program")

	if len(os.Args) < 2 {
		log.WithError(errors.New("not enough arguments")).Fatalln("usage: ./collector [CONFIG_FILE].yaml")
	}

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
	log.Debugln(spew.Sdump(viper.AllSettings()))
	log.Debugln("--------")

	// checking for missing keys
	missingKeys := []string{}
	if !viper.IsSet("influx.database") {
		missingKeys = append(missingKeys, "database")
	}
	if !viper.IsSet("influx.address") {
		missingKeys = append(missingKeys, "address")
	}
	if !viper.IsSet("sets") {
		missingKeys = append(missingKeys, "sets")
	}
	if len(missingKeys) > 0 {
		log.WithField("missing_keys", strings.Join(missingKeys, ",")).Fatalln("missing keys in config")
	}

	var sets []Set
	viper.UnmarshalKey("sets", &sets)

	// trimming directories
	for i := range sets {
		for j, directoryMapping := range sets[i].DirectoryMappings {
			// sets[i].DirectoryMappings[j].External, err = filepath.Abs(directoryMapping.External)
			// if err != nil {
			// 	log.WithError(err).WithField("directory", sets[i].DirectoryMappings[j].External).Fatalln("error getting absolute path for external directory")
			// }
			sets[i].DirectoryMappings[j].Internal, err = filepath.Abs(directoryMapping.Internal)
			if err != nil {
				log.WithError(err).WithField("directory", sets[i].DirectoryMappings[j].Internal).Fatalln("error getting absolute path for internal directory")
			}
		}
	}

	// expanding directories to desired depth
	log.Infoln("expanding directories to desired depth")
	for i, set := range sets {
		for j := 0; j < set.Depth; j++ {
			newDirMappingSlice := []DirectoryMapping{}
			for _, directoryMapping := range sets[i].DirectoryMappings {
				subdirs, err := os.ReadDir(directoryMapping.Internal)
				if err != nil {
					log.WithError(err).WithField("directory", directoryMapping.Internal).Fatalln("error reading directory for expansion")
				}
				for _, subdir := range subdirs {
					if subdir.IsDir() {
						newDirMapping := DirectoryMapping{
							External: filepath.Join(directoryMapping.External, subdir.Name()),
							Internal: filepath.Join(directoryMapping.Internal, subdir.Name()),
						}
						log.WithField("new_mapping", newDirMapping).Debugln("appending to new mapping")
						newDirMappingSlice = append(newDirMappingSlice, newDirMapping)
					}
				}
			}
			log.Debugln("updating mapping for %v with sub-mappings", sets[i].DirectoryMappings)
			sets[i].DirectoryMappings = newDirMappingSlice
		}
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
	isDry := viper.GetBool("dry")
	interval := viper.GetDuration("reporting.interval")

	log.WithField("interval", interval.String()).Infoln("starting metrics ticker")
	metricsTicker := time.NewTicker(interval)
	defer metricsTicker.Stop()
	go func() {
		for ; true; <-metricsTicker.C {
			for _, set := range sets {
				tickTime := time.Now()
				logEntry := log.WithTime(tickTime).WithField("set", set.Name)

				logEntry.Infoln("starting scan")

				dirSizeMap := getAllDirSizesInBytes(logEntry, set.DirectoryMappings)

				for k, v := range dirSizeMap {
					logEntry.WithFields(log.Fields{
						"directory_mapping": k,
						"size":              v,
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
					point, err := influx.NewPoint("directory_size_in_bytes", map[string]string{
						"application":    "directory-size-metrics-collector",
						"set":            set.Name,
						"absolute_path":  k.External,
						"directory_path": filepath.Dir(k.External),
						"base_path":      filepath.Base(k.External),
					}, map[string]interface{}{
						"value": v,
					}, tickTime)
					if err != nil {
						logEntry.WithField("directory_mapping", k).WithError(err).Errorln("error creating point for influx, skipping...")
						continue
					}
					logEntry.Debugf("adding point: %s", point.String())

					batchPoints.AddPoint(point)
				}

				if isDry {
					logEntry.Infoln("dry run: skipping reporting")
					continue
				}

				logEntry.Infoln("sending points to influx")
				err = influxClient.Write(batchPoints)
				if err != nil {
					logEntry.WithError(err).Errorln("error writing points to influx")
				}
			}
			log.Infoln("waiting for next interval...")
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

func getAllDirSizesInBytes(logEntry *log.Entry, directoryMappings []DirectoryMapping) map[DirectoryMapping]int64 {

	log.Traceln("starting directories scan")

	directorySizeMap := map[DirectoryMapping]int64{}

	for _, directoryMapping := range directoryMappings {
		log.WithField("directory_mapping", directoryMapping).Traceln("starting directory scan")

		directorySize, err := getSingleDirSizeInBytes(logEntry, directoryMapping.Internal)
		if err != nil {
			log.WithError(err).Errorln("error getting directory size, skipping...")
			continue
		}
		directorySizeMap[directoryMapping] = directorySize

		log.WithField("directory_mapping", directoryMapping).Traceln("finished directory scan")
	}

	log.Traceln("finished directories scan")

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
