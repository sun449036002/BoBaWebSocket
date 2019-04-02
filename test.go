package main

import (
	"fmt"
	"time"
)

func main() {
	now := time.Now()
	m := now.Minute()
	t := 600 - (m % 10)*60 - now.Second()
	fmt.Println(t)
}