package main

import (
	"time"
	"math"
	"regexp"
	"flag"
	"runtime"
	"fmt"
	"os"
	"strings"
	"net/http"
	gourl "net/url"
	"io/ioutil"
	"os/signal"

	"hey/requester"
)

const (                                          // 常量定义，go中同时定义多个变量写法
	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`
	heyUA        = "hey/0.0.1"
)

var (                                            // go.flag 实现了运行时参数指定功能
	m = flag.String("m", "GET", "")
	headers = flag.String("h", "", "")
	body = flag.String("d", "", "")
	bodyFile = flag.String("D", "", "")
	accept      = flag.String("A", "", "")
	contentType = flag.String("T", "text/html", "")
	authHeader  = flag.String("a", "", "")
	hostHeader = flag.String("host", "", "")

	output = flag.String("o", "", "")

	c = flag.Int("c", 50, "")
	n = flag.Int("n", 200, "")
	q = flag.Float64("q", 0, "")
	t = flag.Int("t", 20, "")
	z = flag.Duration("z", 0, "")

	h2   = flag.Bool("h2", false, "")
	cpus = flag.Int("cpus", runtime.GOMAXPROCS(-1), "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	disableRedirects   = flag.Bool("disable-redirects", false, "")
	proxyAddr = flag.String("x", "", "")
)

var usage = `Usage: hey [options...] <url>
Options:
  -n  Number of requests to run. Default is 200.
  -c  Number of requests to run concurrently. Total number of requests cannot
      be smaller than the concurrency level. Default is 50.
  -q  Rate limit, in queries per second (QPS). Default is no rate limit.
  -z  Duration of application to send requests. When duration is reached,
      application stops and exits. If duration is specified, n is ignored.
      Examples: -z 10s -z 3m.
  -o  Output type. If none provided, a summary is printed.
      "csv" is the only supported alternative. Dumps the response
      metrics in comma-separated values format.
  -m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
  -H  Custom HTTP header. You can specify as many as needed by repeating the flag.
      For example, -H "Accept: text/html" -H "Content-Type: application/xml" .
  -t  Timeout for each request in seconds. Default is 20, use 0 for infinite.
  -A  HTTP Accept header.
  -d  HTTP request body.
  -D  HTTP request body from file. For example, /home/user/file.txt or ./file.txt.
  -T  Content-type, defaults to "text/html".
  -a  Basic authentication, username:password.
  -x  HTTP Proxy address as host:port.
  -h2 Enable HTTP/2.
  -host	HTTP Host header.
  -disable-compression  Disable compression.
  -disable-keepalive    Disable keep-alive, prevents re-use of TCP
                        connections between different HTTP requests.
  -disable-redirects    Disable following of HTTP redirects
  -cpus                 Number of used cpu cores.
                        (default for current machine is %d cores)
`

func main(){
	flag.Usage = func() {                                       // 改变默认Usage， 即 -h 输出提示信息
		fmt.Fprint(os.Stderr, fmt.Sprintf(usage, runtime.NumCPU()))      // rnutime.NumCPU 获取当前主机核数
	}

	var hs headerSlice
	flag.Var(&hs, "H", "")    // 将flag绑定到一个变量上

	flag.Parse()        // flag 参数解析

	fmt.Println(flag.NArg())
	if flag.NArg() < 1 {
		usageAndExit("")
	}

	runtime.GOMAXPROCS(*cpus)         // 设置运行时，cpu利用核数
	num := *n
	conc := *c                        // 并发请求运行参数
	q := *q
	dur := *z

	if dur > 0 {                              // 参数规则判断(发送请求的持续时间，如果设定了dur，则忽略n参数)
		num = math.MaxInt32
		if conc <= 0 {                       // conc 并发参数必须大于1
			usageAndExit("-c cannot be smaller than 1.")
		}
	} else {
		if num <= 0 || conc <= 0 {            // num设定的请求数
			usageAndExit("-n and -c cannot be smaller than 1.")
		}

		if num < conc {
			usageAndExit("-n cannot be less than -c.")
		}
	}

	url := flag.Args()[0]                    // 获取运行的第一个参数字符串
	method := strings.ToUpper(*m)           // 将字符串转为大写

	header := make(http.Header)             // 设置Http请求头，make函数在内存开辟一个http.Header的channel并返回类型的引用
	header.Set("Content-Type", *contentType)

	if *headers != "" {
		usageAndExit("Flag '-h' is deprecated, please use '-H' instead.")
	}

	// set any other additional repeatable headers
	for _, h := range hs {
		match, err := parseInputWithRegexp(h, headerRegexp)
		if err != nil {
			usageAndExit(err.Error())
		}
		header.Set(match[1], match[2])
	}

	if *accept != "" {
		header.Set("Accept", *accept)
	}

	// set basic auth if set
	var username, password string
	if *authHeader != "" {
		match, err := parseInputWithRegexp(*authHeader, authRegexp)
		if err != nil {
			usageAndExit(err.Error())
		}
		username, password = match[1], match[2]
	}

	var bodyAll []byte
	if *body != "" {
		bodyAll = []byte(*body)
	}

	if *bodyFile != "" {
		slurp, err := ioutil.ReadFile(*bodyFile)
		if err != nil {
			errAndExit(err.Error())
		}
		bodyAll = slurp
	}

	var proxyURL *gourl.URL
	if *proxyAddr != "" {
		var err error
		proxyURL, err = gourl.Parse(*proxyAddr)
		if err != nil {
			usageAndExit(err.Error())
		}
	}

	req, err := http.NewRequest(method, url, nil)    //http 内置方法构建http请求
	if err != nil {
		usageAndExit(err.Error())
	}
	req.ContentLength = int64(len(bodyAll))             // 设置请求body
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)            // 若有认证步骤，则设置username、password
	}

	// set host header if set
	if *hostHeader != "" {
		req.Host = *hostHeader                         // http 请求代理设置
	}

	ua := req.UserAgent()                             //http 请求UA设置
	if ua == "" {
		ua = heyUA
	} else {
		ua += " " + heyUA
	}
	header.Set("User-Agent", ua)
	req.Header = header

	w := &requester.Work{                             // 采用requester，按照具体参数构建http请求
		Request:            req,                      // http newRequest
		RequestBody:        bodyAll,                  //具体请求body内容
		N:                  num,                      // 请求总数量
		C:                  conc,                     // 并发数
		QPS:                q,                        //每秒最大请求数
		Timeout:            *t,                       //请求超时时间
		DisableCompression: *disableCompression,      //是否启用压缩
		DisableKeepAlives:  *disableKeepAlives,       //是否启用保活
		DisableRedirects:   *disableRedirects,
		H2:                 *h2,
		ProxyAddr:          proxyURL,                 //是否采用代理
		Output:             *output,
	}
	w.Init()

	c := make(chan os.Signal, 1)        // go中信号处理
	signal.Notify(c, os.Interrupt)      //系统监听signal信号
	go func() {
		<-c
		w.Stop()
	}()
	if dur > 0 {
		go func() {
			time.Sleep(dur)
			w.Stop()
		}()
	}
	w.Run()
}

func errAndExit(msg string) {
	fmt.Fprintf(os.Stderr, msg)
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(1)
}

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, msg)
		fmt.Fprintf(os.Stderr, "\n\n")
	}
	flag.Usage()
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(1)
}

func parseInputWithRegexp(input, regx string) ([]string, error) {
	re := regexp.MustCompile(regx)
	matches := re.FindStringSubmatch(input)
	if len(matches) < 1 {
		return nil, fmt.Errorf("could not parse the provided input; input = %v", input)
	}
	return matches, nil
}

type headerSlice []string              // go 数组定义方式

func (h *headerSlice) String() string {    // go 数组方法定义
	return fmt.Sprintf("%s", *h)
}

func (h *headerSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}