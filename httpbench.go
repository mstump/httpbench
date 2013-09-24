package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
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
	requests             int64
	testDuration         int64
	url                  string
	urls                 []string
	urlsFilePath         string
}

type ConnectionWrapper struct {
	net.Conn
	readTimeout  time.Duration
	writeTimeout time.Duration
	result       *Result
}

type Result struct {
	requests        int64
	success         int64
	networkFailed   int64
	badFailed       int64
	readThroughput  int64
	writeThroughput int64
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
		reportInterval:       5000,
		testDuration:         60000,
		url:                  "",
		urls:                 make([]string, 0),
		urlsFilePath:         "",
		requests:             int64((1 << 63) - 1)}
}

func parseArgs(config *Configuration) {
	flag.Int64Var(&config.requests, "r", -1, "Total number of requests per client")
	flag.IntVar(&config.concurrentClients, "c", 10, "Number of concurrent clients")
	flag.StringVar(&config.hostsString, "h", "", "Optional comma delimited list of HTTP hosts which will be prepended to URLs")
	flag.StringVar(&config.urlsFilePath, "f", "", "URLs file path (line seperated)")
	flag.StringVar(&config.url, "u", "", "URL to use for requests in lieu of an input file")
	flag.BoolVar(&config.httpKeepAlive, "k", true, "Do HTTP keep-alive")
	flag.StringVar(&config.httpPostDataFilePath, "d", "", "HTTP POST data file path")
	flag.Int64Var(&config.testDuration, "t", -1, "Test duration")
	flag.IntVar(&config.httpConnectTimeout, "tc", 5000, "Connect timeout (in milliseconds)")
	flag.IntVar(&config.httpWriteTimeout, "tw", 5000, "Write timeout (in milliseconds)")
	flag.IntVar(&config.httpReadTimeout, "tr", 5000, "Read timeout (in milliseconds)")
	flag.Parse()

	if config.hostsString != "" {
		config.hosts = strings.Split(config.hostsString, ",")
	}

}

func checkConfiguration(config *Configuration) error {
	if config.urlsFilePath == "" && config.url == "" {
		return errors.New("A URL was not specified via the commandline, nor was an input file provided")
	}

	if config.requests == -1 && config.testDuration == -1 {
		return errors.New("Neither a request count, or test duration was provided")
	}

	if config.requests != -1 && config.testDuration != -1 {
		return errors.New("Only one should be provided: [requests|duration]")
	}

	if config.httpPostDataFilePath != "" {
		config.httpMethod = "POST"
		data, err := ioutil.ReadFile(config.httpPostDataFilePath)
		if err != nil {
			return fmt.Errorf("Error reading post data file: %s", err)
		}

		config.httpPostData = data
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

		myConn := &ConnectionWrapper{Conn: conn, readTimeout: readTimeout, writeTimeout: writeTimeout, result: result}

		return myConn, nil
	}
}

func client(config *Configuration, result *Result, done *sync.WaitGroup) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("caught recover: ", r)
			os.Exit(1)
		}
	}()

	myclient := HTTPClient(result, time.Duration(config.httpConnectTimeout)*time.Millisecond,
		time.Duration(config.httpReadTimeout)*time.Millisecond,
		time.Duration(config.httpWriteTimeout)*time.Millisecond)

	hostsLength := len(config.hosts)

	for result.requests < config.requests {
		for _, tmpUrl := range config.urls {
			if hostsLength != 0 {
				tmpUrl = config.hosts[random(0, hostsLength)] + tmpUrl
			}
			req, _ := http.NewRequest(config.httpMethod, tmpUrl, bytes.NewReader(config.httpPostData))

			if config.httpKeepAlive == true {
				req.Header.Add("Connection", "keep-alive")
			} else {
				req.Header.Add("Connection", "close")
			}

			// startTime := time.Now()
			resp, err := myclient.Do(req)
			// endTime := time.Now()
			result.requests++

			if err != nil {
				result.networkFailed++
				continue
			}

			_, errRead := ioutil.ReadAll(resp.Body)

			if errRead != nil {
				result.networkFailed++
				continue
			}

			if resp.StatusCode == http.StatusOK {
				result.success++
			} else {
				result.badFailed++
			}

			resp.Body.Close()
		}
	}

	done.Done()
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

	// start the profiler
	http.ListenAndServe("localhost:6060", nil)

	// startTime := time.Now()
	var done sync.WaitGroup
	results := make(map[int]*Result)

	signalChannel := make(chan os.Signal, 2)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGTERM)
	go func() {
		_ = <-signalChannel
		// printResults(results, startTime)
		os.Exit(0)
	}()

	flag.Parse()

	// set the max number of process
	goMaxProcs := os.Getenv("GOMAXPROCS")
	if goMaxProcs == "" {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}

	fmt.Printf("Dispatching %d clients\n", config.concurrentClients)

	var clientGroup sync.WaitGroup

	clientGroup.Add(config.concurrentClients)
	for i := 0; i < config.concurrentClients; i++ {
		result := &Result{}
		results[i] = result
		go client(config, result, &done)
	}

	fmt.Println("Waiting for results...")
	clientGroup.Wait()
	//	printResults(results, startTime)
}
