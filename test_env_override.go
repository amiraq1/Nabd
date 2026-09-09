package main

import (
	"fmt"
	"nabd/internal/config"
)

func main() {
	fmt.Println("ROUTES:", config.Get("NABD_ROUTES"))
}
