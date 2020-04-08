package log_Process

import (
"fmt"
"log"
"math/rand"
"os"
"time"
)


func main() {
	/**
	127.0.0.1 - - [14/May/2018:14:52:45 +0800] "GET /foo?query=t HTTP/1.1" 404 27 "-" "KeepAliveClient" "-" 1.005 1.854
	*/

	file, err := os.OpenFile("access.log", os.O_WRONLY|os.O_APPEND, os.ModePerm)
	if err != nil {
		panic(fmt.Sprintf("Open file err: %s", err.Error()))
	}
	defer file.Close()

	for {
		for i := 1; i < 4; i++ {
			now := time.Now()
			rand.Seed(now.UnixNano())
			paths := []string{"/foo", "/bar", "/baz", "/qux", "/foo", "/bar", "/bar", "/bar"}
			path := paths[rand.Intn(len(paths))]

			requestTime := rand.Float64()
			if path == "/foo" {
				requestTime = requestTime + 1.4
			}

			upstreamTime := requestTime*0.854

			scheme := "http"
			if now.UnixNano()/1000%2 == 1 {
				scheme = "https"
			}
			dateTime := now.Format("02/Jan/2006:15:04:05")
			code := 200
			if now.Unix()%10 == 1 {
				code = 500
			}

			bytesSend := rand.Intn(1000) + 500
			if path == "/foo" {
				bytesSend = bytesSend + 1000
			}

			line := fmt.Sprintf("172.0.0.12 - - [%s +0000] %s \"GET %s HTTP/1.0\" %d %d \"-\" \"KeepAliveClient\" \"-\" %.3f %.3f\n", dateTime, scheme, path, code, bytesSend, upstreamTime,requestTime)
			_, err := file.Write([]byte(line))
			if err != nil {
				log.Println("writeToFile error:", err)
			}
		}
		time.Sleep(time.Millisecond * 200)
	}
}