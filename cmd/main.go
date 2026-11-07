package main

import (
	"Transcoder/cmd/consumer"
	"log"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	var d consumer.Dispatcher
	err := d.Start()

	//ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	//defer stop()

	if err != nil {
		log.Fatal(err)
	}
}
