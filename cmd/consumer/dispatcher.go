package consumer

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"Transcoder/pkg/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

const consumerPrefetch = 3

var (
	ErrDispatcherNotInitialized = errors.New("dispatcher is not initialized")
	ErrDeliveryStreamClosed     = errors.New("RabbitMQ delivery stream closed")
)

type Dispatcher struct {
	client *Client
	log    *logger.Logger
}

func NewDispatcher() (*Dispatcher, error) {
	client, err := NewClient()
	if err != nil {
		return nil, fmt.Errorf("create RabbitMQ consumer: %w", err)
	}

	return &Dispatcher{
		client: client,
		log:    logger.NewLogger("Dispatcher"),
	}, nil
}

func (d *Dispatcher) Start() error {
	if d == nil || d.client == nil || d.log == nil {
		return ErrDispatcherNotInitialized
	}

	d.log.Infof("starting dispatcher (prefetch=%d)", consumerPrefetch)
	defer func() {
		d.log.Infof("dispatcher stopped; releasing RabbitMQ resources")
		_ = d.client.Close()
	}()

	deliveries, err := d.client.Consume(consumerPrefetch)
	if err != nil {
		if d.client.isClosing() {
			d.log.Infof("dispatcher stopped before message consumption began")
			return nil
		}

		d.log.Errorf("start dispatcher consumption: %v", err)
		return fmt.Errorf("start dispatcher consumption: %w", err)
	}

	d.log.Infof("dispatcher is waiting for RabbitMQ deliveries")

	var wg sync.WaitGroup
	for delivery := range deliveries {
		d.log.Infof("received delivery tag=%d", delivery.DeliveryTag)

		wg.Add(1)
		go d.handle(delivery, &wg)
	}

	d.log.Infof("RabbitMQ delivery stream closed; waiting for in-flight handlers")
	wg.Wait()

	if d.client.isClosing() {
		d.log.Infof("dispatcher shutdown completed")
		return nil
	}

	d.log.Errorf("RabbitMQ delivery stream closed unexpectedly")
	return ErrDeliveryStreamClosed
}

func (d *Dispatcher) handle(delivery amqp.Delivery, wg *sync.WaitGroup) {
	defer wg.Done()

	d.log.Infof("processing delivery tag=%d", delivery.DeliveryTag)
	//TODO: Simulation  to be remove later
	time.Sleep(5 * time.Second)
	d.log.Infof("processed delivery tag=%d body=%q", delivery.DeliveryTag, string(delivery.Body))

	if err := delivery.Ack(false); err != nil {
		if d.client.isClosing() {
			d.log.Warnf("could not acknowledge delivery tag=%d during shutdown; RabbitMQ will requeue it: %v", delivery.DeliveryTag, err)
			return
		}

		d.log.Errorf("acknowledge delivery tag=%d: %v", delivery.DeliveryTag, err)
		return
	}

	d.log.Infof("acknowledged delivery tag=%d", delivery.DeliveryTag)
}

func (d *Dispatcher) Stop() error {
	if d == nil || d.client == nil || d.log == nil {
		return ErrDispatcherNotInitialized
	}

	d.log.Infof("shutdown requested; closing RabbitMQ consumer")
	return d.client.Close()
}
