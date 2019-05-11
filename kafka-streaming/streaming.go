package kafka_streaming

import (
	"fmt"
	"github.com/confluentinc/confluent-kafka-go/kafka"
	"log"
	"time"
)

func CreateConsumerAndSubscribe(server string, groupID string, offset string, topic string) *kafka.Consumer {
	c, err := kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers": server,
		"group.id":          groupID,
		"auto.offset.reset": offset,
	})
	if err != nil {
		panic(err)
	}
	err = c.SubscribeTopics([]string{topic, "^aRegex.*[Tt]opic"}, nil)
	if err != nil {
		panic(err)
	}
	return c
}

func CloseConsumer(c *kafka.Consumer) {
	err := c.Close()
	logErr(err)
}

func CreateProducer(server string) *kafka.Producer {
	p, err := kafka.NewProducer(&kafka.ConfigMap{"bootstrap.servers": server})
	logErr(err)
	return p
}

func ProduceMessage(p kafka.Producer, outputTopic string, outputMessage []byte) {
	// Delivery report handler for produced messages
	go func() {
		for e := range p.Events() {
			switch ev := e.(type) {
			case *kafka.Message:
				if ev.TopicPartition.Error != nil {
					fmt.Printf("Delivery failed: %v\n", ev.TopicPartition)
				} else {
					fmt.Printf("Delivered message to %v\n", ev.TopicPartition)
				}
			}
		}
	}()

	// Produce messages to topic (asynchronously)
	err := p.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &outputTopic, Partition: kafka.PartitionAny},
		Value:          outputMessage,
		Timestamp:      time.Now(),
	}, nil)
	logErr(err)
	// Wait for message deliveries before shutting down
	p.Flush(1 * 1000)
}

func logErr(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
