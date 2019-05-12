package main

import (
	"DEchallenge/kafka-streaming"
	"encoding/json"
	"fmt"
	"github.com/clarkduvall/hyperloglog"
	"github.com/confluentinc/confluent-kafka-go/kafka"
	"gopkg.in/yaml.v2"
	"hash/fnv"
	"io/ioutil"
	"log"
	"sort"
	"strings"
	"time"
)

type Configuration struct {
	InputTopic       string        `yaml:"inputTopic"`
	OutputTopic      string        `yaml:"outputTopic"`
	ReadTimeout      time.Duration `yaml:"readTimeout"`
	Interval         string        `yaml:"interval"`
	BootstrapServers string        `yaml:"bootstrapServers"`
	GroupID          string        `yaml:"groupID"`
	AutoOffsetReset  string        `yaml:"autoOffsetReset"`
}

// Read and parse config.yml
func (c *Configuration) ReadConfig() *Configuration {
	yamlFile, err := ioutil.ReadFile("config.yml")
	logErr(err)
	err = yaml.Unmarshal(yamlFile, c)
	logErr(err)
	return c
}

// Convert unixtime to appropriate string based on provided interval in config.yml
func UnixtimeToString(unixTime int64, interval string) string {
	t := time.Unix(unixTime, 0)
	interval = strings.ToLower(interval)
	switch interval {
	case "second":
		return t.Format("Jan _2 15:04:05 2006")
	case "minute":
		return t.Format("Jan _2 15:04 2006")
	case "hour":
		return t.Format("Jan _2 15 2006")
	case "day":
		return t.Format("Jan _2 2006")
	case "week":
		year, week := t.ISOWeek()
		//return strconv.FormatInt(int64(year), 10) + " " + strconv.FormatInt(int64(week), 10)
		return fmt.Sprintf("%d %d", year, week)
	case "month":
		return t.Format("Jan 2006")
	case "year":
		return t.Format("2006")
	default:
		panic(fmt.Sprintf("provided interval '%s' is not implemented", interval))
	}
}

type InputMessage struct {
	TimeStamp int64  `json:"ts"`
	UniqueID  string `json:"uid"`
}

type outputMessage struct {
	Date  string
	Count uint64
}

type hash64 []byte

// Hash function for HyperLogLog
func (f hash64) Sum64() uint64 {
	q := fnv.New64a() // what is the difference between new64 and new64a
	_, _ = q.Write(f)
	return q.Sum64()
}

// Checks if new message is one of the 0.1% of the messages that comes with more than 5s delay.
// Those messages will be ignored
func isNew(newcomer string, keys []string) bool {
	if len(keys) == 0 {
		return true
	}
	sort.Strings(keys)
	latest := keys[0]
	// since we don't have timezones lexical order should work - double check this!!!
	if newcomer > latest {
		return true
	}
	return false
}

func outputToKafka(datesInMap []string, dateUnixtime map[string]int64, container map[string]*hyperloglog.HyperLogLogPlus, p kafka.Producer, config Configuration) []string {
	if len(datesInMap) > 2 {
		sort.Strings(datesInMap)
		if dateUnixtime[datesInMap[0]]-dateUnixtime[datesInMap[1]] < 5 {
			key := datesInMap[0]
			count := container[key].Count()
			fmt.Println(key, count)
			outputJSON, _ := json.Marshal(outputMessage{key, count})
			kafka_streaming.ProduceMessage(p, config.OutputTopic, outputJSON)
			datesInMap = datesInMap[1:]
			delete(container, key)
			delete(dateUnixtime, key)
		}
	}
	return datesInMap
}

func ParseInputJSON(msg []byte, interval string) (InputMessage, string) {
	// parse input message
	var input InputMessage
	err := json.Unmarshal(msg, &input)
	logErr(err)
	// date need for HyperLogLog
	date := UnixtimeToString(input.TimeStamp, interval)
	return input, date
}

func logErr(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// Streams data from input topic, counts unique users per interval and output data to output topic
func main() {
	// read config yaml and parse it
	var config Configuration
	config.ReadConfig()

	// data structure for counting
	var container map[string]*hyperloglog.HyperLogLogPlus
	container = make(map[string]*hyperloglog.HyperLogLogPlus)

	// DS to keep map ordered and help outputting asap
	var dateUnixtime map[string]int64
	dateUnixtime = make(map[string]int64)
	var datesInMap []string

	// consumer
	c := *kafka_streaming.CreateConsumerAndSubscribe(
		config.BootstrapServers,
		config.GroupID,
		config.AutoOffsetReset,
		config.InputTopic,
	)
	defer kafka_streaming.CloseConsumer(&c)

	// producer
	p := *kafka_streaming.CreateProducer(config.BootstrapServers)
	defer p.Close()

	fmt.Printf("Counting unique IDs from topic %s started\n", config.InputTopic)
	// read messages from the input topic
	for {
		msg, err := c.ReadMessage(time.Second * config.ReadTimeout)
		if err == nil {
			input, date := ParseInputJSON(msg.Value, config.Interval)
			var hashID hash64 = []byte(input.UniqueID)
			hll, ok := container[date]
			if ok {
				hll.Add(hashID)
				if input.TimeStamp > dateUnixtime[date] {
					dateUnixtime[date] = input.TimeStamp
					// time to output?
				}
			} else if isNew(date, datesInMap) {
				hllEstimator, _ := hyperloglog.NewPlus(18)
				container[date] = hllEstimator
				dateUnixtime[date] = input.TimeStamp
				datesInMap = append(datesInMap, date)
			}
			datesInMap = outputToKafka(datesInMap, dateUnixtime, container, p, config)
		} else {
			fmt.Printf("Consumer error: %v (%v)\n", err, msg)
			break
		}

	}
	// Output data left in map
	for _, key := range datesInMap {
		outputJSON, _ := json.Marshal(outputMessage{key, container[key].Count()})
		kafka_streaming.ProduceMessage(p, config.OutputTopic, outputJSON)
	}
	fmt.Println("Counting done. Exiting...")
}
