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
2. `>>> go run Main.go`

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
    represents 31.5.2014., our map will be `{'31.5.2104.':222222}`. This will allow us to
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
| 10000      | 9999    |
| 50000      | 50000   |
| 100000     | 100001  |

So it looks that algorithm is pretty accurate. In the readme from the hyperloglog repo they reported relative error of
0.0045 when cardinality is up to 10000 and 0.006 when cardinality is up to 80000. 

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
I was curious to see how this would work so I've created prototype and put it under parallel directory so you can
take a look if you want. 
**Disclaimer**: This is just proof of concept, I am aware that there is a lot of duplicated code and
other staff that could be done better. 
Speed up was from 32s to 20s (3 consumers used) so we were able to process ~50000 frames per second.


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
go testing package. More specific, I created mini benchmark function (can be found in Main_test.go) where variable bm is one message 
from input topic in bytes and it gave me the following:
```$xslt
>>>go test -run none -bench . -benchtime 1s -benchmem -cpu 1
   goos: darwin
   goarch: amd64
   pkg: DEchallenge
   BenchmarkParseInputJSON           100000             22280 ns/op             312 B/op          8 allocs/op
   PASS
   ok      DEchallenge     2.461s

```
So we were able to perform 100000 conversions in 1s, 22280 ns/op which means that one conversions took us 0.02 millisecond
which should be okay.

#### Rough time estimation
Maybe you are interested, so here is how I spent my time:
1. One week learning Go - I used https://golang.org/ (blog and getting started) and HeadFirst Go
2. One day studying about HyperLogLog, mostly paper mentioned above
3. One weekend to implement solution and write this report



