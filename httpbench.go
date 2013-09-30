package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"github.com/bmizerany/perks/quantile"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Configuration struct {
	concurrentClients    int
	hosts                []string
	hostsString          string
	httpConnectTimeout   int
	httpKeepAlive        bool
	httpMethod           string
	httpPostData         []byte
	httpPostDataFilePath string
	httpReadTimeout      int
	httpWriteTimeout     int
	reportInterval       int64
	testDuration         int64
	urlString            string
	urls                 []string
	urlsFilePath         string
}

type Result struct {
	host             string
	url              string
	start            time.Time
	duration         time.Duration
	httpStatus       int
	responseBodySize int64
	requestBodySize  int64
	networkFailed    bool
	error            error
}

type Metrics struct {
	startTime         time.Time
	total             int64
	success           int64
	httpFailed        int64
	networkFailed     int64
	totalResponseSize int64
	totalRequestSize  int64
	quantiles         *quantile.Stream
}

func NewMetrics() *Metrics {
	return &Metrics{
		startTime:         time.Now(),
		total:             0,
		success:           0,
		httpFailed:        0,
		networkFailed:     0,
		totalResponseSize: 0,
		totalRequestSize:  0,
		quantiles:         quantile.NewTargeted(0.50, 0.95, 0.99),
	}
}

func (this *Metrics) AddResult(result *Result) {
	this.total++

	if result.networkFailed {
		this.networkFailed++
	} else if result.httpStatus != http.StatusOK {
		this.httpFailed++
	} else {
		this.totalResponseSize += result.responseBodySize
		this.totalRequestSize += result.requestBodySize
		this.success++
	}

	this.quantiles.Insert(float64(result.duration.Nanoseconds() / int64(time.Microsecond)))
}

func (this *Metrics) P50() float64 {
	return this.quantiles.Query(0.50)
}

func (this *Metrics) P95() float64 {
	return this.quantiles.Query(0.95)
}

func (this *Metrics) P99() float64 {
	return this.quantiles.Query(0.99)
}

func (this *Metrics) ResetQuantiles() {
	this.quantiles.Reset()
}

func metricsUpdater(config *Configuration, metrics *Metrics, quit chan bool, results chan Result) {
	tick := time.NewTicker(time.Duration(config.reportInterval) * time.Second)

	for {
		select {
		case <-tick.C:
			printStatus(config, metrics)
			metrics.ResetQuantiles()
		case <-quit:
			return
		case r := <-results:
			metrics.AddResult(&r)
		}
	}
}

func (this *Result) Reset() {
	this.host = ""
	this.url = ""
	this.start = time.Unix(0, 0)
	this.duration = 0
	this.httpStatus = 0
	this.responseBodySize = 0
	this.requestBodySize = 0
	this.networkFailed = false
	this.error = nil
}

type ConnectionWrapper struct {
	net.Conn
	readTimeout  time.Duration
	writeTimeout time.Duration
	result       *Result
}

func (this *ConnectionWrapper) Read(b []byte) (n int, err error) {
	len, err := this.Conn.Read(b)

	if err == nil {
		this.result.responseBodySize += int64(len)
		this.Conn.SetReadDeadline(time.Now().Add(this.readTimeout))
	}

	return len, err
}

func (this *ConnectionWrapper) Write(b []byte) (n int, err error) {
	len, err := this.Conn.Write(b)

	if err == nil {
		this.result.requestBodySize += int64(len)
		this.Conn.SetWriteDeadline(time.Now().Add(this.writeTimeout))
	}

	return len, err
}

func NewConfiguration() *Configuration {
	return &Configuration{
		concurrentClients:    10,
		hosts:                make([]string, 0),
		httpConnectTimeout:   5000,
		httpKeepAlive:        true,
		httpMethod:           "GET",
		httpPostData:         make([]byte, 0),
		httpPostDataFilePath: "",
		httpReadTimeout:      5000,
		httpWriteTimeout:     5000,
		reportInterval:       5,
		testDuration:         60000,
		urlString:            "",
		urls:                 make([]string, 0),
		urlsFilePath:         ""}
}

func parseArgs(config *Configuration) {
	flag.Int64Var(&config.reportInterval, "r", 5, "How often to report statistics in seconds")
	flag.IntVar(&config.concurrentClients, "c", 10, "Number of concurrent clients")
	flag.StringVar(&config.hostsString, "h", "", "Optional comma delimited list of HTTP hosts which will be prepended to URLs")
	flag.StringVar(&config.urlsFilePath, "f", "", "URLs file path (line seperated)")
	flag.StringVar(&config.urlString, "u", "", "URL to use for requests in lieu of an input file")
	flag.BoolVar(&config.httpKeepAlive, "k", true, "Do HTTP keep-alive")
	flag.StringVar(&config.httpPostDataFilePath, "d", "", "HTTP POST data file path")
	flag.Int64Var(&config.testDuration, "t", -1, "Test duration")
	flag.IntVar(&config.httpConnectTimeout, "tc", 5000, "Connect timeout (in milliseconds)")
	flag.IntVar(&config.httpWriteTimeout, "tw", 5000, "Write timeout (in milliseconds)")
	flag.IntVar(&config.httpReadTimeout, "tr", 5000, "Read timeout (in milliseconds)")
	flag.Parse()

	config.testDuration = config.testDuration * time.Second.Nanoseconds()

	if config.hostsString != "" {
		config.hosts = strings.Split(config.hostsString, ",")
	}

	if config.urlsFilePath != "" {
		fileLines, err := readLines(config.urlsFilePath)

		if err != nil {
			log.Fatalf("Error in ioutil.ReadFile for file: %s Error: ", config.urlsFilePath, err)
		}

		config.urls = fileLines
	}

	if config.urlString != "" {
		config.urls = append(config.urls, config.urlString)
	}

	if config.httpPostDataFilePath != "" {
		config.httpMethod = "POST"
		data, err := ioutil.ReadFile(config.httpPostDataFilePath)
		if err != nil {
			log.Fatalf("Error reading post data file: %s", err)
		}

		config.httpPostData = data
	}
}

func checkConfiguration(config *Configuration) error {
	if len(config.urls) == 0 {
		return errors.New("A URL was not specified via the commandline, nor was an input file provided")
	}

	if config.testDuration == -1 {
		return errors.New("Required param test duration was not provided")
	}

	return nil
}

func readLines(path string) (lines []string, err error) {
	var file *os.File
	var part []byte
	var prefix bool

	if file, err = os.Open(path); err != nil {
		return
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	buffer := bytes.NewBuffer(make([]byte, 0))
	for {
		if part, prefix, err = reader.ReadLine(); err != nil {
			break
		}
		buffer.Write(part)
		if !prefix {
			lines = append(lines, buffer.String())
			buffer.Reset()
		}
	}

	if err == io.EOF {
		err = nil
	}
	return
}

func random(min, max int) int {
	rand.Seed(time.Now().Unix())
	return rand.Intn(max-min) + min
}

func HTTPClient(result *Result, connectTimeout, readTimeout, writeTimeout time.Duration) *http.Client {

	return &http.Client{
		Transport: &http.Transport{
			Dial:            TimeoutDialer(result, connectTimeout, readTimeout, writeTimeout),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func TimeoutDialer(result *Result, connectTimeout, readTimeout, writeTimeout time.Duration) func(net, address string) (conn net.Conn, err error) {
	return func(mynet, address string) (net.Conn, error) {
		conn, err := net.DialTimeout(mynet, address, connectTimeout)
		if err != nil {
			return nil, err
		}

		conn.SetReadDeadline(time.Now().Add(readTimeout))
		conn.SetWriteDeadline(time.Now().Add(writeTimeout))

		return &ConnectionWrapper{Conn: conn, readTimeout: readTimeout, writeTimeout: writeTimeout, result: result}, nil
	}
}

func client(config *Configuration, quit chan bool, results chan Result, done *sync.WaitGroup) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("caught recover: ", r)
			os.Exit(1)
		}
	}()

	defer func() {
		done.Done()
	}()

	var result Result
	httpclient := HTTPClient(&result, time.Duration(config.httpConnectTimeout)*time.Millisecond,
		time.Duration(config.httpReadTimeout)*time.Millisecond,
		time.Duration(config.httpWriteTimeout)*time.Millisecond)

	hostsLength := len(config.hosts)
	urlsLength := len(config.urls)

	for {
		select {
		case <-quit:
			return
		default:
		}

		tmpUrl := config.urls[random(0, urlsLength)]
		result.Reset()

		if hostsLength > 0 {
			result.host = config.hosts[random(0, hostsLength)]
			tmpUrl = result.host + tmpUrl
		}
		req, _ := http.NewRequest(config.httpMethod, tmpUrl, bytes.NewReader(config.httpPostData))
		result.url = tmpUrl
		result.host = req.Host

		if config.httpKeepAlive == true {
			req.Header.Add("Connection", "keep-alive")
		} else {
			req.Header.Add("Connection", "close")
		}

		startTime := time.Now()
		resp, err := httpclient.Do(req)
		result.duration = time.Now().Sub(startTime)
		result.start = startTime

		if err != nil {
			result.networkFailed = true
			result.error = err
			results <- result
			continue
		}

		_, errRead := ioutil.ReadAll(resp.Body)

		if errRead != nil {
			result.networkFailed = true
			result.error = errRead
			results <- result
			continue
		}

		result.httpStatus = resp.StatusCode
		resp.Body.Close()
		results <- result
	}
}

func printStatus(config *Configuration, metrics *Metrics) {
	successRate := 0.0
	httpFailRate := 0.0
	netFailRate := 0.0
	sendThroughput := 0.0
	receiveThroughput := 0.0

	if metrics.total != 0 {
		successRate = float64(metrics.success / metrics.total)
		httpFailRate = float64(metrics.httpFailed / metrics.total)
		netFailRate = float64(metrics.networkFailed / metrics.total)
		sendThroughput = float64(metrics.totalResponseSize/config.reportInterval) / 1024.0
		receiveThroughput = float64(metrics.totalResponseSize/config.reportInterval) / 1024.0
	}

	now := time.Now()

	fmt.Printf("%s | seconds : %08.0f | total : %010d | succes : %1.2f | http failure : %1.2f | network failure : %1.2f | receive : %4.2f kB/s | send : %4.2f kB/s | P50 : %2.2f µs | P95 : %2.2f µs | P99 : %2.2f µs\n",
		now.Format("02 Jan 2006 15:04:05"),
		now.Sub(metrics.startTime).Seconds(),
		metrics.total,
		successRate,
		httpFailRate,
		netFailRate,
		receiveThroughput,
		sendThroughput,
		metrics.P50(),
		metrics.P95(),
		metrics.P99())
}

func main() {
	config := NewConfiguration()
	parseArgs(config)
	err := checkConfiguration(config)
	if err != nil {
		log.Println(err)
		flag.Usage()
		os.Exit(1)
	}

	startTime := time.Now()
	metrics := NewMetrics()
	quit := make(chan bool)

	// start the profiler
	// http.ListenAndServe("localhost:6060", nil)

	signalChannel := make(chan os.Signal, 2)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGTERM)
	go func() {
		_ = <-signalChannel
		os.Exit(0)
	}()

	// set the max number of process
	goMaxProcs := os.Getenv("GOMAXPROCS")
	if goMaxProcs == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	var clientGroup sync.WaitGroup

	// spawn the metric updater
	results := make(chan Result, 100000)
	go metricsUpdater(config, metrics, quit, results)

	// spawn the clients
	clientGroup.Add(config.concurrentClients)
	for i := 0; i < config.concurrentClients; i++ {
		go client(config, quit, results, &clientGroup)
	}

	// wait for test duration
	for {
		if time.Now().Sub(startTime).Nanoseconds() > config.testDuration {
			close(quit)
			break
		}
		time.Sleep(1)
	}

	clientGroup.Wait()
	printStatus(config, metrics)
}
