//go:build swapbook

package main

import (
	"log"
	"net/http"
	"os"

	dashboard "github.com/bahe-msft/osb-dashboard"
)

func main() {
	addr := os.Getenv("SWAPBOOK_TARGET_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8081"
	}
	handler, err := dashboard.NewSwapbookHandler()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Swapbook target listening on http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}
