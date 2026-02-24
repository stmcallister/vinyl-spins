package main

import (
	"fmt"

	"discogs-listen-tracker/backend/internal/demobig"
)

// Demo reporting binary to increase build size.
func main() {
	fmt.Printf("report: %d\n", demobig.Touch())
}

