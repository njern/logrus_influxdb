package logrus_influxdb

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Sirupsen/logrus"
	influxdb "github.com/influxdb/influxdb/client"
)

const (
	DefaultHost     = "localhost" // default InfluxDB hostname
	DefaultPort     = 8086        // default InfluxDB Port
	DefaultDatabase = "logrus"
)

// InfulxDBHook delivers logs to an InfluxDB cluster.
type InfulxDBHook struct {
	client   *influxdb.Client
	database string
	tags     map[string]string
}

// NewInfulxDBHook creates a hook to be added to an instance of logger and initializes the InfluxDB client
func NewInfluxDBHook(hostname string, database string, tags map[string]string) (*InfulxDBHook, error) {
	// use the default database if we're missing one in the initialization
	if database == "" {
		database = DefaultDatabase
	}

	if tags == nil {
		tags = map[string]string{}
	}

	u, err := url.Parse(fmt.Sprintf("http://%s:%d", hostname, DefaultPort))
	if err != nil {
		return nil, err
	}
	conf := influxdb.Config{
		URL:      *u,
		Username: os.Getenv("INFLUX_USER"), // detect InfluxDB environment variables
		Password: os.Getenv("INFLUX_PWD"),
		Timeout:  100 * time.Millisecond, // Default timeout of 100 milliseconds
	}

	client, err := influxdb.NewClient(conf)
	if err != nil {
		return nil, err
	}

	// Try pinging InfluxDB to see if it's a valid connection
	_, _, err = client.Ping()
	if err != nil {
		return nil, err
	}

	hook := &InfulxDBHook{client, database, tags}

	err = hook.autocreateDatabase()
	if err != nil {
		return nil, err
	}

	return hook, nil
}

// NewWithClientInfulxDBHook creates a hook using an initialized InfluxDB client.
func NewWithClientInfulxDBHook(client *influxdb.Client, database string, tags map[string]string) (*InfulxDBHook, error) {
	// use the default database if we're missing one in the initialization
	if database == "" {
		database = DefaultDatabase
	}

	if tags == nil {
		tags = map[string]string{}
	}

	// If the configuration is nil then assume default configurations
	if client == nil {
		return NewInfluxDBHook(DefaultHost, database, tags)
	}
	return &InfulxDBHook{client, database, tags}, nil
}

// Called when an event should be sent to InfluxDB
func (hook *InfulxDBHook) Fire(entry *logrus.Entry) error {
	point := influxdb.Point{
		Measurement: "logrus",
		Tags:        hook.tags, // set the default tags from Hook
		Fields: map[string]interface{}{
			"message": entry.Message,
		},
		Time:      time.Now(),
		Precision: "s",
	}

	// Set the level of the entry
	point.Tags["level"] = entry.Level.String()

	// getAndDel and getAndDelRequest are taken from https://github.com/evalphobia/logrus_sentry
	if logger, ok := getField(entry.Data, "logger"); ok {
		point.Tags["logger"] = logger
	}
	if serverName, ok := getField(entry.Data, "server_name"); ok {
		point.Tags["server_name"] = serverName
	}
	if req, ok := getRequest(entry.Data, "http_request"); ok {
		point.Fields["http_request"] = req
	}
	point.Fields["extras"] = map[string]interface{}(entry.Data)

	_, err := hook.client.Write(influxdb.BatchPoints{
		Points:          []influxdb.Point{point},
		Database:        hook.database,
		RetentionPolicy: "default",
	})
	if err != nil {
		return err
	}
	return nil
}

// queryDB convenience function to query the database
func (hook *InfulxDBHook) queryDB(cmd string) ([]influxdb.Result, error) {
	response, err := hook.client.Query(influxdb.Query{
		Command:  cmd,
		Database: hook.database,
	})
	if err != nil {
		return nil, err
	}
	if response.Error() != nil {
		return nil, response.Error()
	}
	return response.Results, nil
}

// Return back an error if the database does not exist in InfluxDB
func (hook *InfulxDBHook) databaseExists() error {
	results, err := hook.queryDB("SHOW DATABASES")
	if err != nil {
		return err
	}
	if results == nil || len(results) == 0 {
		return errors.New("Missing results from InfluxDB query response")
	}
	if results[0].Series == nil || len(results[0].Series) == 0 {
		return errors.New("Missing series from InfluxDB query response")
	}
	for _, value := range results[0].Series[0].Values {
		for _, val := range value {
			if v, ok := val.(string); ok { // InfluxDB returns back an interface. Try to check only the string values.
				if v == hook.database { // If we the database exists, return back nil errors
					return nil
				}
			}
		}
	}
	return errors.New("No database exists")
}

// Try to detect if the database exists and if not, automatically create one.
func (hook *InfulxDBHook) autocreateDatabase() error {
	err := hook.databaseExists()
	if err == nil {
		return nil
	}
	_, err = hook.queryDB(fmt.Sprintf("create database %s", hook.database))
	if err != nil {
		return err
	}
	return nil
}

// Try to return a field from logrus
// Taken from Sentry adapter (from https://github.com/evalphobia/logrus_sentry)
func getField(d logrus.Fields, key string) (string, bool) {
	var (
		ok  bool
		v   interface{}
		val string
	)
	if v, ok = d[key]; !ok {
		return "", false
	}

	if val, ok = v.(string); !ok {
		return "", false
	}
	return val, true
}

// Try to return an http request
// Taken from Sentry adapter (from https://github.com/evalphobia/logrus_sentry)
func getRequest(d logrus.Fields, key string) (*http.Request, bool) {
	var (
		ok  bool
		v   interface{}
		req *http.Request
	)
	if v, ok = d[key]; !ok {
		return nil, false
	}
	if req, ok = v.(*http.Request); !ok || req == nil {
		return nil, false
	}
	return req, true
}

// Available logging levels.
func (hook *InfulxDBHook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
		logrus.DebugLevel,
	}
}