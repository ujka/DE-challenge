package main

import (
	"encoding/json"
	"fmt"
	"github.com/clarkduvall/hyperloglog"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"parallel/kafka-streaming"
	"time"
)

type OM struct {
	Date       string
	EncodedHLL []byte
}

type MergerConfiguration struct {
	InputTopic       string        `yaml:"inputTopic"`
	OutputTopic      string        `yaml:"outputTopic"`
	ReadTimeout      time.Duration `yaml:"readTimeout"`
	Interval         string        `yaml:"interval"`
	BootstrapServers string        `yaml:"bootstrapServers"`
	GroupID          string        `yaml:"groupID"`
	AutoOffsetReset  string        `yaml:"autoOffsetReset"`
	Consumers        int           `yaml:"consumers"`
}

type finalMessage struct {
	Date  string
	Count uint64
}

// Read and parse config.yml
func (c *MergerConfiguration) ReadMConfig() *MergerConfiguration {
	yamlFile, err := ioutil.ReadFile("configMerger.yml")
	if err != nil {
		log.Fatal(err)
	}
	err = yaml.Unmarshal(yamlFile, c)
	if err != nil {
		log.Fatal(err)
	}
	return c
}

// Streams data from input topic, counts unique users per interval and output data to output topic
func main() {
	// read config yaml and parse it
	var config MergerConfiguration
	config.ReadMConfig()

	// data structure for counting
	var container map[string]*hyperloglog.HyperLogLogPlus
	container = make(map[string]*hyperloglog.HyperLogLogPlus)

	// DS to keep map ordered and help outputting asap
	var dateCount map[string]int
	dateCount = make(map[string]int)
	var deleteSlice []string

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

	fmt.Printf("Reading from topic %s started\n", config.InputTopic)
	// read messages from the input topic
	for {
		msg, err := c.ReadMessage(time.Second * config.ReadTimeout)
		if err == nil {
			// parse input message
			var input OM
			err := json.Unmarshal(msg.Value, &input)
			if err != nil {
				log.Fatal(err)
			}
			hll, ok := container[input.Date]
			if ok {
				newHLL, _ := hyperloglog.NewPlus(18)
				_ = newHLL.GobDecode(input.EncodedHLL)
				_ = hll.Merge(newHLL)
				dateCount[input.Date] += 1
			} else {
				hllEstimator, _ := hyperloglog.NewPlus(18)
				_ = hllEstimator.GobDecode(input.EncodedHLL)
				container[input.Date] = hllEstimator
				dateCount[input.Date] = 1
			}
			for key, value := range dateCount {
				if value == config.Consumers {
					deleteSlice = append(deleteSlice, key)
				}
			}
			if len(deleteSlice) > 0 {
				for _, key := range deleteSlice {
					count := container[key].Count()
					fmt.Println(key, count)
					outputJSON, _ := json.Marshal(finalMessage{key, count})
					kafka_streaming.ProduceMessage(p, config.OutputTopic, outputJSON)
					delete(container, key)
					delete(dateCount, key)
				}
				deleteSlice = deleteSlice[0:0]
			}

		} else {
			fmt.Printf("Consumer error: %v (%v)\n", err, msg)
			break
		}
	}
}
