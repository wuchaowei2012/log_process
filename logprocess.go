package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/influxdata/influxdb/client/v2"
	_ "github.com/influxdata/influxdb/client/v2"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Reader interface {
	Read(rc chan []byte)
}

type Writer interface {
	Write(wc chan *Message)
}

type ReadFromFile struct {
	path string // log file path
}

type WriteToInfluxDB struct {
	influxDBDsn string // influx data destination
}

type Message struct {
	TimeLocal                    time.Time
	ByteSent                     int
	Path, Method, Schema, Status string
	UpstreamTime, RequestTime    float64
}

type LogProcess struct {
	rc    chan []byte
	wc    chan *Message
	read  Reader
	write Writer
}

// 系统状态监控
type SystemInfo struct {
	HandleLine   int     `json:"handleLine"`   // 总处理日志行数
	Tps          float64 `json:"tps"`          //系统吞吐量
	ReadChanlen  int     `json:"readChanlen"`  //读信道长度
	WriteChanlen int     `json:"writeChanlen"` //写信道长度
	RunTime      string  `json:"runTime"`      //总运行时间
	ErrNum       int     `json:"errNum"`       //错误数
}

type Monitor struct {
	startTime time.Time
	data      SystemInfo
	tpSli     []int
}

func (m *Monitor) start(lp *LogProcess) {
	go func(){
		for n := range TypeMonitorChan{
			switch n {
			case TypeErrNum:
				m.data.ErrNum +=1
			case TypeHandleLine:
				m.data.HandleLine +=1
			}
		}
	}()

	// 以下代码使用定时器，实现tps提取
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for{
			<- ticker.C // // The channel on which the ticks are delivered.
			m.tpSli = append(m.tpSli, m.data.HandleLine)
			if len(m.tpSli)>2 {
				m.tpSli = m.tpSli[1:]
			}
		}
	}()

	http.HandleFunc("/monitor", func(writer http.ResponseWriter, request *http.Request) {
		m.data.RunTime = time.Now().Sub(m.startTime).String()
		m.data.ReadChanlen = len(lp.rc)
		m.data.WriteChanlen = len(lp.wc)
		if len(m.tpSli) >= 2 {
			m.data.Tps = float64(m.tpSli[1]-m.tpSli[0]) / 5.0
		}
		// // Marshal returns the JSON encoding of v.
		ret, _ := json.MarshalIndent(m.data, "", "\t")
		io.WriteString(writer, string(ret))
	})

	http.ListenAndServe(":9193", nil)
}

const (
	TypeHandleLine = 0
	TypeErrNum     = 1
)

var TypeMonitorChan = make(chan int, 200)

func (r *ReadFromFile) Read(rc chan []byte) {
	// log reading module

	//打开文件
	f, err := os.Open(r.path)

	if err != nil {
		TypeMonitorChan <- TypeErrNum
		panic(fmt.Sprintf("open file error: %s", err.Error()))
	}

	// 从末尾开始读取文件内容
	// Seek sets the offset for the next Read or Write on file to offset, interpreted
	// according to whence: 0 means relative to the origin of the file, 1 means
	// relative to the current offset, and 2 means relative to the end.
	f.Seek(0, 2)

	// NewReader returns a new Reader whose buffer has the default size.
	rd := bufio.NewReader(f)

	for {
		line, err := rd.ReadBytes('\n')

		if err == io.EOF {
			time.Sleep(500 * time.Millisecond)
			continue
		} else if err != nil {
			TypeMonitorChan <- TypeErrNum
			panic(fmt.Sprintf("readbytes error: %s", err.Error()))
		}
		// 正常处理的日记行数
		TypeMonitorChan <- TypeHandleLine
		rc <- line[:len(line)-1]
	}
}

func (l *LogProcess) Process() {
	// log parsing module
	//                           172.0.0.12                -         - [08/Apr/2020:18:17:02 +0000]  http "GET /bar HTTP/1.0" 200 730 "-" "KeepAliveClient" "-" 0.612 0.717
	r := regexp.MustCompile(`([\d\.]+[\d])\s+([^\[]+)\s+([^\[]+)\s+\[([^\]]+)\]\s+([^\]]+)\s+\"([^"]+)\"\s+(\d{3})\s+(\d+)\s+\"([^"]+)\"\s+\"([^"]+)\"\s+\"([^"]+)\"\s+([\d]+\.[\d]+)\s+([\d]+\.[\d]+)`)

	// LoadLocation returns the Location with the given name.
	// If the name is "" or "UTC", LoadLocation returns UTC.
	// If the name is "Local", LoadLocation returns Local.

	loc, _ := time.LoadLocation("Asia/Shanghai")
	for v := range l.rc {

		// FindAllStringSubmatch is the 'All' version of FindStringSubmatch; it
		// returns a slice of all successive matches of the expression, as defined by
		// the 'All' description in the package comment.
		// A return value of nil indicates no match.
		ret := r.FindStringSubmatch(string(v))

		if len(ret) != 14 {
			TypeMonitorChan <- TypeErrNum
			log.Println("FindStringSubmatch fails:", string(v))
			log.Println("ret length:", len(ret))
			continue
		}
		message := &Message{}

		// ParseInLocation is like Parse but differs in two important ways.
		// First, in the absence of time zone information, Parse interprets a time as UTC;
		// ParseInLocation interprets the time as in the given location.
		// Second, when given a zone offset or abbreviation, Parse tries to match it
		// against the Local location; ParseInLocation uses the given location.
		//t, err := time.ParseInLocation("02/Jan/2006:15:04:05 +0800", ret[4], loc)
		t, err := time.ParseInLocation("02/Jan/2006:15:04:05 +0000", ret[4], loc)

		if err != nil {
			TypeMonitorChan <- TypeErrNum
			log.Println("ParseInLocation fails:", ret[4])
			continue
		}

		//   172.0.0.12 - - [08/Apr/2020:18:17:02 +0000]  http "GET /bar HTTP/1.0" 200 730 "-" "KeepAliveClient" "-" 0.612 0.717
		message.TimeLocal = t

		reqSli := strings.Split(ret[6], " ")
		if len(reqSli) != 3 {
			TypeMonitorChan <- TypeErrNum
			log.Println("url related strings.Split fail:", ret[5])
			continue
		}
		message.Method = reqSli[0]

		//解析 path
		u, err := url.Parse(reqSli[1])
		if err != nil {
			log.Println("url parse fail", err)
			continue
		}
		message.Path = u.Path

		message.Status = ret[7]

		// byteSent
		byteSent, _ := strconv.Atoi(ret[8])
		message.ByteSent = byteSent

		upstreamTime, _ := strconv.ParseFloat(ret[12], 64)
		requestTime, _ := strconv.ParseFloat(ret[13], 64)
		message.UpstreamTime = upstreamTime
		message.RequestTime = requestTime

		l.wc <- message
	}
}

//func (w *WriteToInfluxDB) Write(wc chan *Message) {
//	// log write modul
//	for i := range wc {
//		fmt.Println(i)
//	}
//}

func (w *WriteToInfluxDB) Write(wc chan *Message) {
	// log writing module

	infSli := strings.Split(w.influxDBDsn, "@")

	// create a new http client
	c, err := client.NewHTTPClient(client.HTTPConfig{
		Addr:     infSli[0],
		Username: infSli[1],
		Password: infSli[2],
	})
	if err != nil {
		TypeMonitorChan <- TypeErrNum
		log.Fatal("error related with create client ", err)
	}
	defer c.Close()

	// // NewBatchPoints returns a BatchPoints interface based on the given config.
	bp, err := client.NewBatchPoints(client.BatchPointsConfig{
		Precision: infSli[4],
		Database:  infSli[3],
	})
	if err != nil {
		TypeMonitorChan <- TypeErrNum
		log.Fatal("error related with create NewBatchPoints ", err)
	}

	for v := range wc {
		// create a point and add it to a batch
		// tags 实质上是有索引的属性
		tags := map[string]string{
			"Path": v.Path, "Method": v.Method, "Scheme": v.Schema, "Status": v.Status,
		}
		// fields 为各种记录的值
		fields := map[string]interface{}{
			"UpstreamTime": v.UpstreamTime,
			"RequestTime":  v.RequestTime,
			"BytesSent":    v.ByteSent,
		}

		// points 为表里面的一行数据
		pt, err := client.NewPoint("nginx_log", tags, fields, v.TimeLocal)
		if err != nil {
			TypeMonitorChan <- TypeErrNum
			log.Fatal("error related with create NewPoint ", err)
		}

		// time 数据记录的时间戳，也是自动生成的主索引
		bp.AddPoint(pt)

		// Write the batch
		if err := c.Write(bp); err != nil {
			TypeMonitorChan <- TypeErrNum
			log.Fatal("error related with write batch to fluxDb", err)
		}

		log.Println("write success")

	}

}

func main() {
	var path, influxDsn string
	flag.StringVar(&path, "path", "access.log", "read file path")

	// http://127.0.0.1:8086@wangxu@wangxu26@imooc@s
	flag.StringVar(&influxDsn, "influxDsn", "http://127.0.0.1:8086@fred@@fred@s", "influx data destination")

	flag.Parse()

	r := &ReadFromFile{path: path}
	w := &WriteToInfluxDB{influxDBDsn: influxDsn}

	lp := &LogProcess{
		//使用带缓存的 channel， 防止阻塞
		rc:    make(chan []byte, 200),
		wc:    make(chan *Message, 200),
		read:  r,
		write: w,
	}

	// 打开两个 goroutines
	for i := 0; i < 2; i++ {
		go lp.read.Read(lp.rc)
	}

	go lp.Process()

	// 打开两个 goroutines
	for i := 0; i < 4; i++ {
		go lp.write.Write(lp.wc)
	}

	//time.Sleep(1000 * time.Second)

	m := Monitor{
		startTime:time.Now(),
		data:SystemInfo{},
	}
	m.start(lp)
}
