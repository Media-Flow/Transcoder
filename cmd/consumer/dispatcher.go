package consumer

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"Transcoder/pkg/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Dispatcher struct {
	client *Client
	log    *logger.Logger
}

func (d *Dispatcher) Start() error {
	if d.client == nil {
		client, err := NewClient()
		if err != nil {
			return err
		}
		d.client = client
	}

	defer d.client.Close()

	if d.log == nil {
		d.log = logger.NewLogger("dispatcher")
	}

	deliveries, err := d.client.Consume(3)
	if err != nil {
		d.log.Errorf("start dispatcher: %v", err)
		return err
	}

	var wg sync.WaitGroup

	for delivery := range deliveries {
		wg.Add(1)
		go handle(delivery, &wg)
	}
	wg.Wait()

	return errors.New("deliveries channel closed")
}

func handle(delivery amqp.Delivery, wg *sync.WaitGroup) {
	defer wg.Done()

	time.Sleep(time.Second * 5)

	fmt.Println(string(delivery.Body))
	delivery.Ack(false)
	// create  context
	// invoke processing
	// ack or nack
}
