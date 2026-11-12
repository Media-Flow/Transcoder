package consumer

import (
	"Transcoder/internal/media"
	"Transcoder/internal/transcoder"
	"Transcoder/pkg/logger"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

const consumerPrefetch = 3

var (
	ErrDispatcherNotInitialized = errors.New("dispatcher is not initialized")
	ErrDeliveryStreamClosed     = errors.New("RabbitMQ delivery stream closed")
)

type Dispatcher struct {
	client       *Client
	transcoder   *transcoder.Transcoder
	mediaService *media.Service
	log          *logger.Logger
}

func NewDispatcher(transcoderService *transcoder.Transcoder, mediaService *media.Service) (*Dispatcher, error) {
	client, err := NewClient()
	if err != nil {
		return nil, fmt.Errorf("create RabbitMQ consumer: %w", err)
	}

	return &Dispatcher{
		client:       client,
		transcoder:   transcoderService,
		mediaService: mediaService,
		log:          logger.NewLogger("Dispatcher"),
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
	ctx, closer := context.WithCancel(context.Background())
	defer closer()

	d.log.Infof("processed delivery tag=%d body=%q", delivery.DeliveryTag, string(delivery.Body))

	var msg EventMessage
	if err := json.Unmarshal(delivery.Body, &msg); err != nil {
		d.log.Errorf("unmarshal delivery tag=%d: %v", delivery.DeliveryTag, err)
		_ = delivery.Nack(false, false)
		return
	}

	if msg.EventId == uuid.Nil || msg.MediaID == uuid.Nil || msg.Payload.SourceKey == "" {
		d.log.Errorf("invalid delivery tag=%d: missing IDs or source key", delivery.DeliveryTag)
		_ = delivery.Nack(false, false)
		return
	}

	variants := []media.TranscodeVariant{
		{VariantKey: media.VariantKey1080p},
		{VariantKey: media.VariantKey720p},
	}
	result, err := d.mediaService.HandleTranscodeEvent(ctx, msg.EventId, msg.MediaID, variants)
	if err != nil {
		d.log.Errorf("failed to process delivery tag=%d: %v", delivery.DeliveryTag, err)
		_ = delivery.Nack(false, true)
		return
	}

	switch result.Outcome {
	case media.TranscodeOutcomeProcess:
		// continue to transcode below

	case media.TranscodeOutcomeDeadLetter:
		// TODO: push into DLQ before ack
		d.ack(delivery)
		return

	case media.TranscodeOutcomeIgnored:
		d.ack(delivery)
		return

	default:
		d.log.Errorf("unknown outcome tag=%d: %v", delivery.DeliveryTag, result.Outcome)
		_ = delivery.Nack(false, false)
		return
	}

	if result.Event == nil {
		d.log.Errorf("nil event tag=%d", delivery.DeliveryTag)
		_ = delivery.Nack(false, false)
		return
	}

	// start processing and check for failure
	if err = d.transcoder.Transcode(
		ctx,
		result.Event,
		msg.Payload.SourceKey,
	); err != nil {
		d.log.Errorf("transcode tag=%d: %v", delivery.DeliveryTag, err)
		_ = delivery.Nack(false, true)
		return
	}

	d.ack(delivery)
}

func (d *Dispatcher) ack(delivery amqp.Delivery) {
	if err := delivery.Ack(false); err != nil {
		if d.client.isClosing() {
			d.log.Warnf("could not acknowledge tag=%d during shutdown: %v", delivery.DeliveryTag, err)
			return
		}

		d.log.Errorf("acknowledge tag=%d: %v", delivery.DeliveryTag, err)
		return
	}

	d.log.Infof("acknowledged tag=%d", delivery.DeliveryTag)
}

func (d *Dispatcher) Stop() error {
	if d == nil || d.client == nil || d.log == nil {
		return ErrDispatcherNotInitialized
	}

	d.log.Infof("shutdown requested; closing RabbitMQ consumer")
	return d.client.Close()
}
