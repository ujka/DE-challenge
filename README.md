# Data engineer challenge 

Since the task was to count unique users per interval and using naive solution with set 
as data structure would consume too much memory, I decided to use HyperLogLog++. I came across 
this [paper](https://storage.googleapis.com/pub-tools-public-publication-data/pdf/40671.pdf)
I studied it deeply and made sure to understand how it works. As suggested in the task hints 
`tap into other peoples know-how and code`, I didn't want to reinvent
the wheel so I found HyperLogLog++ implementation [here](https://github.com/clarkduvall/hyperloglog). 
I am sure that you guys know how it works so I am not going to explain that here, or why it's suitable
for this problem, but if you want to talk about this algorithm in more detail I'm looking
forward to it :)

#### Running script
1. Open config.yml and configure it
2. go run Main.go

#### Idea of the solution:
1. Create data structures that will be used to reach business requirements:
    * map named container - datetime is key and struct HyperLogLog++ is value
    * map named dateUnixtime - datetime is key and unixtime is value
2. Read the data from the kafka input
3. Parse input message to struct with timestamp and uniqueID as fields
4. Convert timestamp(unixtime) to appropriate datetime(string) based on desired interval
5. Add new key, value pairs to maps
    * adding to container map is straightforward 
    * when adding to dateUnixtime map, keep maximum unixtime values that are mapped
    to specific datetime. E.g. if 1111111 unixtime represents 31.5.2014. and 222222 also
    represents 31.5.2014., our map will be {'31.5.2104.':222222}. This will allow us to
    output data to kafka as soon as possible.
6. Check if it's time to output counting result to kafka 
    * since the requirement is to output data as soon as possible we will check how many
    datetimes we have in our dateUnixtime map. If we have only one, obviously it's not the time to
    output it. If we have 2 datetimes(keys) in dateUnixtime map, we compare values of those 
    datetimes(unixtime correspoding to the datetime). If the difference between unixtime values is bigger than 5s
    it's time to count unique users for earlier datetime and output it, because of the assumption
    from the taks `You can assume that 99.9% of the frames arrive with a maximum latency of 5 seconds`
    Also, this means that we decided to ignore 0.1% frames that are too late. 
7. If it's output time, appropriate json is created (with datetime and unique id count) and outputted
to the output topic.

#### Benchmarks:

1. Since the HLL++ is estimating cardinality of a set, first I tested to see how accurate
that estimation is (There is also benchmark on the github page but I wanted to test it anyhow).
Code to produce data
```func main() {
 	p := *kafka_streaming.CreateProducer("localhost:9092")
 	for i:= 0; i<1000; i++ {
 		outputJSON, _ := json.Marshal(IM{1868984393, strconv.Itoa(i)})
 		kafka_streaming.ProduceMessage(p, "bench10", outputJSON)
 	}
```
and here are the results:

| Unique Ids | Counted |
|------------|---------|
| 10         | 9       |
| 50         | 49      |
| 100        | 99      |
| 250        | 249     |
| 500        | 499     |
| 1000       | 999     |
| 50000      | 5000    |
| 100000     | 10001   |



2. To measure frames per second I've used the following
```
>>> time go run Main.go 
Counting unique IDs from topic input_topic1 started
Consumer error: Local: Timed out (<nil>)
Delivered message to output_topic[0]@1
Counting done. Exiting...

real	0m36.884s
user	0m29.728s
sys	0m1.706s
```
and input_topic1 has been fed with `gzcat stream.gz | kafka-console-producer --broker-list localhost:9092 --topic input_topic1`
so it has 1,000,000 messages. 
Difference between real - user - sys ~ 5s is because of consumer read time out setting which
is set to 5s. So we can conclude that processing 1,000,000 frames took us around 32s 
which leads to ~ 31250 frames per second.

3. 

#### Bonus questions / Challenges
* `how do you scale it to improve troughput.` - library that I used for HLL has a very nice answer to
this, it provides us with three methods `Merge`, `GobEncode` and `GobDecode`. 
So one way to scale could be:
1. When creating input_topic it should have more than 1 partition
2. Additional topic should be created, let's call it `middle`
3. Instead of calling `Count` method we would encode our HLL with `GobEncode` and send json message that looks like
{"date":"some date", "hll":encodedHLL} to middle topic.
4. New consumer should be created, it should be aware of number of consumers from the previous step.
It will decode HLLs, merge them using `Merge` and when sure that all consumers sent their
data to `middle`, `Count` for particular datetime, and output result to the output topic in similar
manner that we know.
I was curious to see how this would work so I've created prototype and put it to another repo so you can
take a look if you want. 
Speed up was from 32s to 20s (3 consumers were used) so we were able to process 50000 frames per second.


* `you may want to count things for different time frames but only do json parsing once.` - 
this is possible by making more maps for each time frame that we are interested in. The rest
of the processing is the same.
E.g. let's say we are interested in statistics for minutes and hours. We will create
for minute - containerMinute, dateUnixtimeMinute, datesInMapMinute (ideally we will make
new struct that holds everything we need for specific time frame) and same for hours.
We will parse json, take unixtime stamp, convert to minute/hour date string and the
rest is completely independent and it could even run as a separate goroutine.

* `explain how you would cope with failure if the app crashes mid day / mid year.` - Once again 
we are saved by `GobEncode` and `GobDecode`. We could periodically (after every
nth read message or every x minutes) call `GobEncode` and save it with additional data (partition, topic, offset, time frame)
to the database. So if our app crashes we could restore and continue from the checkpoint.

* `when creating e.g. per minute statistics, how do you handle frames that arrive late or frames with a random timestamp (e.g. hit by a bitflip), describe a strategy?`
Since in our dateUnixtime map we have appropriate datetime and maximum unixtime that maps to that datetime, 
we can compare newcomers to maximum unixtime seen so far, and set some threshold so when `abs(max_unixtime - new_unixtime) > threshold` we will
ignore that message. This is already happening in `isNew` method but for now we are only
checking if the message is too old. 

* `measure also the performance impact / overhead of the json parser` for this I used
go testing package. More specific, I created mini benchmark function where variable bm is one message 
from input topic in bytes
```$xslt
import (
	"encoding/json"
	"testing"
)

type input struct {
	TimeStamp int64  `json:"ts"`
	UniqueID  string `json:"uid"`
}

var bm = []byte{123, 34, 85, 116, 34, 58, 91, 34, 116, 114, 97, 110, 115, 97, 99, 116, 105, 111, 110, 97, 108, 92, 116, 99, 111, 100, 101, 92, 116, 116, 104, 97, 116, 92, 116, 105, 115, 92, 116, 101, 97, 115, 121, 92, 116, 116, 111, 92, 116, 119, 114, 105, 116, 101, 92, 116, 97, 110, 100, 34, 44, 34, 99, 111, 100, 101, 47, 112, 114, 111, 112, 101, 114, 116, 105, 101, 115, 44, 47, 103, 105, 118, 105, 110, 103, 47, 116, 104, 101, 47, 98, 101, 115, 116, 47, 111, 102, 47, 98, 111, 116, 104, 47, 116, 104, 101, 34, 44, 34, 112, 108, 101, 97, 115, 97, 110, 116, 92, 92, 102, 111, 114, 92, 92, 116, 97, 115, 107, 115, 44, 92, 92, 98, 111, 116, 104, 92, 92, 115, 109, 97, 108, 108, 92, 92, 97, 110, 100, 92, 92, 108, 97, 114, 103, 101, 46, 92, 92, 83, 104, 111, 119, 34, 44, 34, 111, 116, 104, 101, 114, 92, 92, 104, 97, 110, 100, 44, 92, 92, 115, 116, 97, 116, 105, 99, 92, 92, 105, 110, 102, 101, 114, 101, 110, 99, 101, 92, 92, 100, 101, 100, 117, 99, 101, 115, 92, 92, 116, 121, 112, 101, 115, 92, 92, 97, 110, 100, 92, 92, 111, 116, 104, 101, 114, 34, 44, 34, 109, 101, 109, 111, 114, 121, 92, 102, 109, 97, 110, 97, 103, 101, 109, 101, 110, 116, 92, 102, 109, 97, 107, 101, 115, 92, 102, 102, 111, 114, 92, 102, 115, 97, 102, 101, 44, 92, 102, 115, 105, 109, 112, 108, 101, 44, 92, 102, 97, 110, 100, 92, 102, 114, 111, 98, 117, 115, 116, 34, 44, 34, 68, 32, 97, 108, 108, 111, 119, 115, 32, 119, 114, 105, 116, 105, 110, 103, 32, 108, 97, 114, 103, 101, 32, 99, 111, 100, 101, 32, 102, 114, 97, 103, 109, 101, 110, 116, 115, 32, 119, 105, 116, 104, 111, 117, 116, 32, 114, 101, 100, 117, 110, 100, 97, 110, 116, 108, 121, 34, 44, 34, 116, 104, 101, 92, 114, 82, 65, 73, 73, 92, 114, 105, 100, 105, 111, 109, 41, 92, 114, 97, 110, 100, 92, 114, 115, 99, 111, 112, 101, 92, 114, 115, 116, 97, 116, 101, 109, 101, 110, 116, 115, 92, 114, 102, 111, 114, 92, 114, 100, 101, 116, 101, 114, 109, 105, 110, 105, 115, 116, 105, 99, 34, 44, 34, 116, 114, 97, 110, 115, 97, 99, 116, 105, 111, 110, 97, 108, 92, 116, 99, 111, 100, 101, 92, 116, 116, 104, 97, 116, 92, 116, 105, 115, 92, 116, 101, 97, 115, 121, 92, 116, 116, 111, 92, 116, 119, 114, 105, 116, 101, 92, 116, 97, 110, 100, 34, 44, 34, 114, 101, 97, 100, 46, 32, 83, 104, 111, 119, 32, 101, 120, 97, 109, 112, 108, 101, 32, 66, 117, 105, 108, 116, 45, 105, 110, 32, 108, 105, 110, 101, 97, 114, 32, 97, 110, 100, 32, 97, 115, 115, 111, 99, 105, 97, 116, 105, 118, 101, 32, 97, 114, 114, 97, 121, 115, 44, 34, 44, 34, 115, 116, 97, 116, 105, 99, 92, 98, 97, 110, 100, 92, 98, 116, 104, 101, 92, 98, 100, 121, 110, 97, 109, 105, 99, 92, 98, 119, 111, 114, 108, 100, 115, 46, 92, 98, 83, 104, 111, 119, 92, 98, 101, 120, 97, 109, 112, 108, 101, 92, 98, 65, 117, 116, 111, 109, 97, 116, 105, 99, 34, 44, 34, 112, 108, 101, 97, 115, 97, 110, 116, 92, 92, 102, 111, 114, 92, 92, 116, 97, 115, 107, 115, 44, 92, 92, 98, 111, 116, 104, 92, 92, 115, 109, 97, 108, 108, 92, 92, 97, 110, 100, 92, 92, 108, 97, 114, 103, 101, 46, 92, 92, 83, 104, 111, 119, 34, 44, 34, 208, 159, 208, 181, 209, 135, 208, 176, 208, 187, 209, 140, 208, 189, 209, 139, 32, 208, 177, 209, 139, 208, 187, 208, 184, 32, 208, 189, 208, 176, 209, 136, 208, 184, 32, 208, 178, 209, 129, 209, 130, 209, 128, 208, 181, 209, 135, 208, 184, 58, 34, 44, 123, 125, 44, 34, 109, 101, 109, 111, 114, 121, 92, 102, 109, 97, 110, 97, 103, 101, 109, 101, 110, 116, 92, 102, 109, 97, 107, 101, 115, 92, 102, 102, 111, 114, 92, 102, 115, 97, 102, 101, 44, 92, 102, 115, 105, 109, 112, 108, 101, 44, 92, 102, 97, 110, 100, 92, 102, 114, 111, 98, 117, 115, 116, 34, 44, 102, 97, 108, 115, 101, 44, 34, 99, 111, 100, 101, 47, 112, 114, 111, 112, 101, 114, 116, 105, 101, 115, 44, 47, 103, 105, 118, 105, 110, 103, 47, 116, 104, 101, 47, 98, 101, 115, 116, 47, 111, 102, 47, 98, 111, 116, 104, 47, 116, 104, 101, 34, 44, 34, 111, 116, 104, 101, 114, 92, 92, 104, 97, 110, 100, 44, 92, 92, 115, 116, 97, 116, 105, 99, 92, 92, 105, 110, 102, 101, 114, 101, 110, 99, 101, 92, 92, 100, 101, 100, 117, 99, 101, 115, 92, 92, 116, 121, 112, 101, 115, 92, 92, 97, 110, 100, 92, 92, 111, 116, 104, 101, 114, 34, 44, 34, 111, 116, 104, 101, 114, 92, 92, 104, 97, 110, 100, 44, 92, 92, 115, 116, 97, 116, 105, 99, 92, 92, 105, 110, 102, 101, 114, 101, 110, 99, 101, 92, 92, 100, 101, 100, 117, 99, 101, 115, 92, 92, 116, 121, 112, 101, 115, 92, 92, 97, 110, 100, 92, 92, 111, 116, 104, 101, 114, 34, 44, 91, 93, 44, 49, 52, 48, 44, 34, 115, 116, 97, 116, 105, 99, 92, 98, 97, 110, 100, 92, 98, 116, 104, 101, 92, 98, 100, 121, 110, 97, 109, 105, 99, 92, 98, 119, 111, 114, 108, 100, 115, 46, 92, 98, 83, 104, 111, 119, 92, 98, 101, 120, 97, 109, 112, 108, 101, 92, 98, 65, 117, 116, 111, 109, 97, 116, 105, 99, 34, 44, 34, 208, 152, 32, 208, 189, 208, 190, 209, 135, 209, 140, 209, 142, 32, 208, 191, 208, 181, 208, 189, 209, 140, 208, 181, 32, 209, 129, 208, 190, 208, 187, 208, 190, 208, 178, 209, 140, 209, 143, 44, 45, 34, 93, 44, 34, 117, 105, 100, 34, 58, 34, 99, 56, 99, 48, 55, 98, 54, 97, 48, 102, 50, 98, 50, 97, 101, 100, 49, 50, 51, 34, 44, 34, 116, 115, 34, 58, 49, 52, 54, 56, 50, 52, 52, 51, 56, 55, 44, 34, 110, 111, 115, 116, 114, 117, 100, 34, 58, 34, 115, 112, 101, 99, 105, 102, 121, 105, 110, 103, 92, 34, 116, 121, 112, 101, 115, 44, 92, 34, 108, 105, 107, 101, 92, 34, 100, 121, 110, 97, 109, 105, 99, 92, 34, 108, 97, 110, 103, 117, 97, 103, 101, 115, 92, 34, 100, 111, 46, 92, 34, 79, 110, 92, 34, 116, 104, 101, 34, 44, 34, 110, 111, 110, 34, 58, 34, 115, 108, 105, 99, 101, 115, 44, 92, 34, 97, 110, 100, 92, 34, 114, 97, 110, 103, 101, 115, 92, 34, 109, 97, 107, 101, 92, 34, 100, 97, 105, 108, 121, 92, 34, 112, 114, 111, 103, 114, 97, 109, 109, 105, 110, 103, 92, 34, 115, 105, 109, 112, 108, 101, 92, 34, 97, 110, 100, 34, 44, 34, 112, 97, 114, 105, 97, 116, 117, 114, 46, 34, 58, 116, 114, 117, 101, 44, 34, 97, 109, 101, 116, 44, 34, 58, 34, 99, 111, 100, 101, 46, 92, 110, 68, 92, 110, 97, 108, 115, 111, 92, 110, 115, 117, 112, 112, 111, 114, 116, 115, 92, 110, 115, 99, 111, 112, 101, 100, 92, 110, 114, 101, 115, 111, 117, 114, 99, 101, 92, 110, 109, 97, 110, 97, 103, 101, 109, 101, 110, 116, 92, 110, 40, 97, 107, 97, 34, 44, 34, 117, 116, 34, 58, 34, 111, 116, 104, 101, 114, 92, 92, 104, 97, 110, 100, 44, 92, 92, 115, 116, 97, 116, 105, 99, 92, 92, 105, 110, 102, 101, 114, 101, 110, 99, 101, 92, 92, 100, 101, 100, 117, 99, 101, 115, 92, 92, 116, 121, 112, 101, 115, 92, 92, 97, 110, 100, 92, 92, 111, 116, 104, 101, 114, 34, 44, 34, 97, 100, 105, 112, 105, 115, 99, 105, 110, 103, 34, 58, 34, 115, 116, 97, 116, 105, 99, 92, 98, 97, 110, 100, 92, 98, 116, 104, 101, 92, 98, 100, 121, 110, 97, 109, 105, 99, 92, 98, 119, 111, 114, 108, 100, 115, 46, 92, 98, 83, 104, 111, 119, 92, 98, 101, 120, 97, 109, 112, 108, 101, 92, 98, 65, 117, 116, 111, 109, 97, 116, 105, 99, 34, 44, 34, 97, 109, 101, 116, 44, 34, 58, 34, 208, 159, 208, 181, 209, 135, 208, 176, 208, 187, 209, 140, 208, 189, 209, 139, 32, 208, 177, 209, 139, 208, 187, 208, 184, 32, 208, 189, 208, 176, 209, 136, 208, 184, 32, 208, 178, 209, 129, 209, 130, 209, 128, 208, 181, 209, 135, 208, 184, 58, 34, 44, 34, 105, 110, 34, 58, 34, 99, 111, 100, 101, 47, 112, 114, 111, 112, 101, 114, 116, 105, 101, 115, 44, 47, 103, 105, 118, 105, 110, 103, 47, 116, 104, 101, 47, 98, 101, 115, 116, 47, 111, 102, 47, 98, 111, 116, 104, 47, 116, 104, 101, 34, 44, 34, 101, 105, 117, 115, 109, 111, 100, 34, 58, 34, 99, 111, 100, 101, 46, 92, 110, 68, 92, 110, 97, 108, 115, 111, 92, 110, 115, 117, 112, 112, 111, 114, 116, 115, 92, 110, 115, 99, 111, 112, 101, 100, 92, 110, 114, 101, 115, 111, 117, 114, 99, 101, 92, 110, 109, 97, 110, 97, 103, 101, 109, 101, 110, 116, 92, 110, 40, 97, 107, 97, 34, 44, 34, 101, 115, 115, 101, 34, 58, 123, 125, 44, 34, 100, 111, 108, 111, 114, 101, 34, 58, 102, 97, 108, 115, 101, 44, 34, 118, 111, 108, 117, 112, 116, 97, 116, 101, 34, 58, 34, 101, 120, 97, 109, 112, 108, 101, 34, 44, 34, 105, 110, 34, 58, 34, 116, 104, 101, 92, 114, 82, 65, 73, 73, 92, 114, 105, 100, 105, 111, 109, 41, 92, 114, 97, 110, 100, 92, 114, 115, 99, 111, 112, 101, 92, 114, 115, 116, 97, 116, 101, 109, 101, 110, 116, 115, 92, 114, 102, 111, 114, 92, 114, 100, 101, 116, 101, 114, 109, 105, 110, 105, 115, 116, 105, 99, 34, 44, 34, 97, 109, 101, 116, 44, 34, 58, 34, 114, 101, 97, 100, 46, 32, 83, 104, 111, 119, 32, 101, 120, 97, 109, 112, 108, 101, 32, 66, 117, 105, 108, 116, 45, 105, 110, 32, 108, 105, 110, 101, 97, 114, 32, 97, 110, 100, 32, 97, 115, 115, 111, 99, 105, 97, 116, 105, 118, 101, 32, 97, 114, 114, 97, 121, 115, 44, 34, 44, 34, 105, 114, 117, 114, 101, 34, 58, 34, 68, 32, 97, 108, 108, 111, 119, 115, 32, 119, 114, 105, 116, 105, 110, 103, 32, 108, 97, 114, 103, 101, 32, 99, 111, 100, 101, 32, 102, 114, 97, 103, 109, 101, 110, 116, 115, 32, 119, 105, 116, 104, 111, 117, 116, 32, 114, 101, 100, 117, 110, 100, 97, 110, 116, 108, 121, 34, 44, 34, 101, 110, 105, 109, 34, 58, 34, 99, 111, 100, 101, 46, 92, 110, 68, 92, 110, 97, 108, 115, 111, 92, 110, 115, 117, 112, 112, 111, 114, 116, 115, 92, 110, 115, 99, 111, 112, 101, 100, 92, 110, 114, 101, 115, 111, 117, 114, 99, 101, 92, 110, 109, 97, 110, 97, 103, 101, 109, 101, 110, 116, 92, 110, 40, 97, 107, 97, 34, 44, 34, 100, 111, 108, 111, 114, 101, 34, 58, 34, 99, 111, 100, 101, 47, 112, 114, 111, 112, 101, 114, 116, 105, 101, 115, 44, 47, 103, 105, 118, 105, 110, 103, 47, 116, 104, 101, 47, 98, 101, 115, 116, 47, 111, 102, 47, 98, 111, 116, 104, 47, 116, 104, 101, 34, 44, 34, 105, 110, 34, 58, 34, 208, 162, 208, 190, 208, 179, 208, 180, 208, 176, 32, 208, 186, 208, 176, 208, 186, 208, 190, 208, 185, 45, 209, 130, 208, 190, 32, 208, 183, 208, 187, 208, 190, 208, 177, 208, 189, 209, 139, 208, 185, 32, 208, 179, 208, 181, 208, 189, 208, 184, 208, 185, 34, 125}

func BenchmarkJSONConversion(b *testing.B) {
	var jsonMessage input
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ =json.Unmarshal(bm, &jsonMessage)
	}
}
```
and it gave me the following:
```$xslt
>>> go test -run none -bench . -benchtime 3s -benchmem -cpu 1
goos: darwin
goarch: amd64
pkg: tamedia
BenchmarkJSONConversion                   200000             20842 ns/op             264 B/op          6 allocs/op
```
# todo: EXPLAIN

#### Rough time estimation
Maybe you are interested, so here is how I spent my time:
1. One week learning Go - I used https://golang.org/ (blog and getting started) and HeadFirst Go
2. One day studying about HyperLogLog, mostly paper mentioned above
3. One weekend to implement solution and write this report







