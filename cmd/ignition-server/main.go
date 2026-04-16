// ignition-server serves a single file over HTTP and auto-exits.
// Usage: ignition-server <file> <port>
package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	data, _ := os.ReadFile(os.Args[1])
	s := &http.Server{Addr: ":" + os.Args[2], Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(data) })}
	go func() { time.Sleep(10 * time.Minute); s.Close() }()
	s.ListenAndServe()
}
