httpbench
=========

HTTP load test engine written in Go. This originally started as a fork of [gobench](https://github.com/cmpxchg16/gobench), but as I got more into it, the fork evolved into a rewrite, hence the name change. The major functionality added is the ability to send load to multiple HTTP servers, and enhanced reporting. In addition to reporting requests per interval and througput we also report latency quantiles of 50%, 95% and 99%.

### Usage
```
Usage of ./httpbench:
  -c=10: Number of concurrent clients
  -cpuprofile="": write cpu profile to file
  -d="": HTTP POST data file path
  -f="": URLs file path (line seperated)
  -h="": Optional comma delimited list of HTTP hosts which will be prepended to URLs
  -k=true: Do HTTP keep-alive
  -r=5: How often to report statistics in seconds
  -t=-1: Test duration
  -tc=5000: Connect timeout (in milliseconds)
  -tr=5000: Read timeout (in milliseconds)
  -tw=5000: Write timeout (in milliseconds)
  -u="": URL to use for requests in lieu of an input file
```

### Example Output
```
./httpbench -u="http://localhost:8983/solr/couponsnextgendb.UserOfferActivated/select?q=*%3A*&wt=xml&indent=true" -r 1 -t 5 -c 1 -cpuprofile=http.prof      
30 Sep 2013 10:54:50 | seconds : 00000001 | total : 0000004202 | succes : 1.00 | http failure : 0.00 | network failure : 0.00 | receive : 2064.85 kB/s | send : 2064.85 kB/s | P50 : 196.00 µs | P95 : 236.00 µs | P99 : 250.00 µs
30 Sep 2013 10:54:51 | seconds : 00000002 | total : 0000008405 | succes : 1.00 | http failure : 0.00 | network failure : 0.00 | receive : 4130.19 kB/s | send : 4130.19 kB/s | P50 : 194.00 µs | P95 : 229.00 µs | P99 : 257.00 µs
30 Sep 2013 10:54:52 | seconds : 00000003 | total : 0000012732 | succes : 1.00 | http failure : 0.00 | network failure : 0.00 | receive : 6256.45 kB/s | send : 6256.45 kB/s | P50 : 192.00 µs | P95 : 225.00 µs | P99 : 234.00 µs
30 Sep 2013 10:54:53 | seconds : 00000004 | total : 0000017121 | succes : 1.00 | http failure : 0.00 | network failure : 0.00 | receive : 8413.20 kB/s | send : 8413.20 kB/s | P50 : 188.00 µs | P95 : 221.00 µs | P99 : 235.00 µs
30 Sep 2013 10:54:54 | seconds : 00000005 | total : 0000021482 | succes : 1.00 | http failure : 0.00 | network failure : 0.00 | receive : 10556.16 kB/s | send : 10556.16 kB/s | P50 : 189.00 µs | P95 : 222.00 µs | P99 : 235.00 µs
```
