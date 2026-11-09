package consumer

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"Transcoder/pkg/config"
	"Transcoder/pkg/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	errNotConnected  = errors.New("consumer is not connected to RabbitMQ")
	errAlreadyClosed = errors.New("consumer is already closed")
)

var ErrQueueUnavailable = errors.New("RabbitMQ queue is not working")

type queueUnavailableError struct {
	operation string
	cause     error
}

func (err *queueUnavailableError) Error() string {
	return fmt.Sprintf("%s: %s: %v", ErrQueueUnavailable, err.operation, err.cause)
}

func (err *queueUnavailableError) Unwrap() error { return err.cause }

func (err *queueUnavailableError) Is(target error) bool {
	return target == ErrQueueUnavailable
}

func unavailable(operation string, cause error) error {
	return &queueUnavailableError{operation: operation, cause: cause}
}

type Client struct {
	queueName  string
	connection *amqp.Connection
	channel    *amqp.Channel
	log        *logger.Logger
	done       chan struct{}

	closeOnce sync.Once
	closeErr  error
}

func NewClient() (*Client, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load consumer config: %w", err)
	}
	_ = logger.InitLogger(cfg)
	log := logger.NewLogger("consumer")

	addr := strings.TrimSpace(cfg.QueueConfig.QueueAddr)
	queueName := strings.TrimSpace(cfg.QueueConfig.QueueName)
	if addr == "" {
		return nil, errors.New("RabbitMQ URL is empty")
	}
	if queueName == "" {
		return nil, errors.New("RabbitMQ queue name is empty")
	}

	conn, err := amqp.Dial(addr)
	if err != nil {
		queueErr := unavailable("connect to RabbitMQ", err)
		log.Errorf("%v", queueErr)
		return nil, queueErr
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		queueErr := unavailable("open RabbitMQ channel", err)
		log.Errorf("%v", queueErr)
		return nil, queueErr
	}

	_, err = ch.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		_ = ch.Close()
		_ = conn.Close()
		queueErr := unavailable(fmt.Sprintf("declare queue %q", queueName), err)
		log.Errorf("%v", queueErr)
		return nil, queueErr
	}

	client := &Client{
		queueName:  queueName,
		connection: conn,
		channel:    ch,
		log:        log,
		done:       make(chan struct{}),
	}
	connClose := conn.NotifyClose(make(chan *amqp.Error, 1))
	channelClose := ch.NotifyClose(make(chan *amqp.Error, 1))
	go client.watchClose(connClose, channelClose)

	log.Infof("connected to RabbitMQ queue %q", queueName)
	return client, nil
}

func (client *Client) watchClose(connClose, channelClose <-chan *amqp.Error) {
	select {
	case err, ok := <-connClose:
		if client.isClosing() {
			return
		}
		if ok && err != nil {
			client.log.Errorf("%v", unavailable("RabbitMQ connection closed", err))
		}
	case err, ok := <-channelClose:
		if client.isClosing() {
			return
		}
		if ok && err != nil {
			client.log.Errorf("%v", unavailable("RabbitMQ channel closed", err))
		}
	case <-client.done:
	}
}

func (client *Client) isClosing() bool {
	if client == nil || client.done == nil {
		return false
	}

	select {
	case <-client.done:
		return true
	default:
		return false
	}
}

func (client *Client) Consume(prefetch int) (<-chan amqp.Delivery, error) {
	if client == nil {
		return nil, unavailable("start consumer", errNotConnected)
	}

	if client.connection == nil || client.channel == nil {
		err := unavailable("start consumer", errNotConnected)
		if client.log != nil {
			client.log.Errorf("%v", err)
		}
		return nil, err
	}

	if client.connection.IsClosed() || client.channel.IsClosed() {
		err := unavailable("start consumer", errAlreadyClosed)
		client.log.Errorf("%v", err)
		return nil, err
	}

	if err := client.channel.Qos(prefetch, 0, false); err != nil {
		queueErr := unavailable("set QoS", err)
		client.log.Errorf("%v", queueErr)
		return nil, queueErr
	}

	deliveries, err := client.channel.Consume(
		client.queueName,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		queueErr := unavailable(fmt.Sprintf("start consumer for queue %q", client.queueName), err)
		client.log.Errorf("%v", queueErr)
		return nil, queueErr
	}

	client.log.Infof("consuming messages from queue %q (prefetch=%d)", client.queueName, prefetch)
	return deliveries, nil
}

func (client *Client) Close() error {
	if client == nil {
		return nil
	}

	client.closeOnce.Do(func() {
		if client.log != nil {
			client.log.Infof("closing RabbitMQ consumer for queue %q", client.queueName)
		}
		if client.done != nil {
			close(client.done)
		}
		if client.channel != nil {
			client.closeErr = client.channel.Close()
		}
		if client.connection != nil {
			if err := client.connection.Close(); client.closeErr == nil {
				client.closeErr = err
			}
		}
		if client.log != nil && client.closeErr != nil {
			client.log.Errorf("close RabbitMQ consumer: %v", client.closeErr)
		} else if client.log != nil {
			client.log.Infof("closed RabbitMQ consumer for queue %q", client.queueName)
		}
	})
	return client.closeErr
}
