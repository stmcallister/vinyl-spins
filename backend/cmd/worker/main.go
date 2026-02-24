package main

import (
	"log"
	"time"

	"discogs-listen-tracker/backend/internal/demobig"
)

// Demo worker binary to increase build size.
func main() {
	start := time.Now()
	n := demobig.Touch()
	log.Printf("worker warmup: %d (took %s)", n, time.Since(start))
}

