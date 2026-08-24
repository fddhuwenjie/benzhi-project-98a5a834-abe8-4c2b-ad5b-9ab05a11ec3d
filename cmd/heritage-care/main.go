package main

import (
	"flag"
	"fmt"
	"heritage-care/internal/httpapi"
	"heritage-care/internal/storage"
	"net/http"
	"net/http/httptest"
	"os"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19081", "监听地址")
	self := flag.Bool("self-check", false, "执行自检后退出")
	data := flag.String("data-dir", "", "JSON持久化目录")
	flag.Parse()
	if p := os.Getenv("PORT"); p != "" {
		*addr = "127.0.0.1:" + p
	}
	store, e := storage.New(*data)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	srv := httpapi.New(store)
	if *self {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/healthz", nil)
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != 200 {
			fmt.Fprintln(os.Stderr, "自检失败")
			os.Exit(1)
		}
		fmt.Printf("自检通过，配置监听地址 %s\n", *addr)
		return
	}
	fmt.Printf("文物预防性养护服务监听 %s\n", *addr)
	if e = http.ListenAndServe(*addr, srv.Handler()); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
