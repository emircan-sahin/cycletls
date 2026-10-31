package main

import (
	"flag"

	"log"
	nhttp "net/http"
	"os"
	"runtime"
	"github.com/Danny-Dasilva/CycleTLS/cycletls"
)


func setupRoutes() {
	nhttp.HandleFunc("/", cycletls.WSEndpoint)
}

func main() {
	port, exists := os.LookupEnv("WS_PORT")
	var addr *string
	// Loopback only: every interface made it an open proxy to anyone who could reach the port.
	if exists {
		addr = flag.String("addr", "127.0.0.1:"+port, "http service address")
	} else {
		addr = flag.String("addr", "127.0.0.1:9112", "http service address")
	}

	runtime.GOMAXPROCS(runtime.NumCPU())

	setupRoutes()
	log.Fatal(nhttp.ListenAndServe(*addr, nil))
}
