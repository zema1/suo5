package main

import (
	"bytes"
	"github.com/kataras/golog"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"
)

func main() {
	start := time.Now()
	http.HandleFunc("/testconn", func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		data, err := io.ReadAll(request.Body)
		if err != nil {
			golog.Errorf("readbody %s", err)
			return
		}
		n, err := writer.Write(data)
		if err != nil {
			golog.Errorf("write err %s", err)
		}
		if n != len(data) {
			golog.Errorf("write not equal, expected %d, got %d", len(data), n)
		}
	})
	go http.ListenAndServe("127.0.0.1:9977", nil)
	time.Sleep(time.Second)
	runReq()
	golog.Infof("total time: %.2f", time.Since(start).Seconds())
}

func runReq() {
	proxy, _ := url.Parse("socks5://127.0.0.1:1111")
	var wg sync.WaitGroup
	// var connDone atomic.Uint32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// defer func() {
			// 	golog.Infof("done %d", connDone.Add(1))
			// }()
			client := http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: time.Second * 5}
			for j := 0; j < 30; j++ {
				data := randBytes()
				resp, err := client.Post("http://127.0.0.1:9977/testconn", "application/octet-stream", bytes.NewReader(data))
				if err != nil {
					golog.Error(err)
					return
				}
				newData, err := io.ReadAll(resp.Body)
				if err != nil {
					golog.Error(err)
					return
				}
				_ = resp.Body.Close()
				if !bytes.Equal(data, newData) {
					golog.Error("data not equal")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func randBytes() []byte {
	randCount := rand.Intn(40960)
	data := make([]byte, randCount)
	rand.Read(data)
	return data
}
