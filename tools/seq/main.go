package main

import (
	"flag"
	"fmt"
	"github.com/KyleYe/open-im-server/v3/tools/seq/internal"
	"time"
)

func main() {
	var (
		config string
		second int
	)
	flag.StringVar(&config, "c", "", "config directory")
	flag.IntVar(&second, "sec", 3600*24, "delayed deletion of the original seq key after conversion")
	flag.Parse()
	if err := internal.Main(config, time.Duration(second)*time.Second); err != nil {
		fmt.Println("seq task", err)
	}
	fmt.Println("seq task success!")
}
