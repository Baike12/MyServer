package kafka

import (
	"context"
	"fmt"
	"myServer/config"
	"myServer/log"
	"strings"
	"sync"
	"time"

	"github.com/Shopify/sarama"
	"go.uber.org/zap"
)

type Kafka struct {
	key      string
	Producer sarama.SyncProducer
	Consumer sarama.Consumer
	Client   sarama.Client
}

var globalKafkaClient sync.Map

func InitKafka() {
	for k, v := range config.Config.Kafka {
		cfg := buildConfig(v)
		kafka, err := newKafkaClient(k, v, cfg)
		if err != nil {
			return
		}
		globalKafkaClient.Store(k, kafka)
	}
}

func buildConfig(v *config.KafkaConf) *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.Producer.RequiredAcks = sarama.RequiredAcks(v.RequiredAck)
	cfg.Producer.Return.Successes = true

	if v.Partition == 1 {
		cfg.Producer.Partitioner = sarama.NewRandomPartitioner
	}

	if v.Partition == 2 {
		cfg.Producer.Partitioner = sarama.NewRoundRobinPartitioner
	}

	if v.ReadTimeout != 0 {
		cfg.Net.ReadTimeout = time.Duration(v.ReadTimeout) * time.Second
	}

	if v.WriteTimeout != 0 {
		cfg.Net.WriteTimeout = time.Duration(v.WriteTimeout) * time.Second
	}

	if v.MaxOpenRequests != 0 {
		cfg.Net.MaxOpenRequests = v.MaxOpenRequests
	}

	return cfg
}

func newKafkaClient(key string, tcfg interface{}, scfg *sarama.Config) (*Kafka, error) {
	cfg := tcfg.(*config.KafkaConf)
	client, err := sarama.NewClient(strings.Split(cfg.Address, ","), scfg)
	if err != nil {
		return nil, err
	}

	syncProducer, err := sarama.NewSyncProducer(strings.Split(cfg.Address, ","), scfg)
	if err != nil {
		return nil, err
	}

	consumer, err := sarama.NewConsumer(strings.Split(cfg.Address, ","), scfg)
	if err != nil {
		return nil, err
	}

	return &Kafka{
		key:      key,
		Client:   client,
		Producer: syncProducer,
		Consumer: consumer,
	}, nil
}

func GetClient(key string) (*Kafka, error) {
	val, ok := globalKafkaClient.Load(key)
	if !ok {
		return nil, fmt.Errorf("get client failed, key: %s", key)
	}
	return val.(*Kafka), nil
}

func SendMessage(ctx context.Context, key, topic, value string) error {
	return SendMessagePartitionPar(ctx, key, topic, value, "")
}

func SendMessagePartitionPar(ctx context.Context, key, topic, value, partitionKey string) error {
	kafka, err := GetClient(key)
	if err != nil {
		return err
	}

	msg := &sarama.ProducerMessage{
		Topic:     topic,
		Value:     sarama.StringEncoder(value),
		Timestamp: time.Now(),
	}

	if partitionKey != "" {
		msg.Key = sarama.StringEncoder(partitionKey)
	}

	partition, offset, err := kafka.Producer.SendMessage(msg)
	if err != nil {
		return nil
	}
	log.DebugLog("send message success", zap.Int32("partition", partition), zap.Int64("offset", offset))
	return err
}

func Consumer(ctx context.Context, key, topic string, fn func(msg *sarama.ConsumerMessage) error) (err error) {
	kafka, err := GetClient(key)
	if err != nil {
		return err
	}

	partiotions, err := kafka.Consumer.Partitions(topic)
	if err != nil {
		return
	}

	for _, partiotion := range partiotions {
		offset, err := kafka.Client.GetOffset(topic, partiotion, sarama.OffsetNewest)
		if err != nil {
			log.InfoLog("get offset failed", zap.Error(err))
			continue
		}

		cp, err := kafka.Consumer.ConsumePartition(topic, partiotion, offset)
		if err != nil {
			log.InfoLog("create partition consumer failed", zap.Error(err))
		}

		// consume message
		go func(c sarama.PartitionConsumer) {
			defer func() {
				if err := recover(); err != nil {
					log.ErrorLog("panic occurred while consuming kafka messages")
				}
			}()

			defer func() {
				err := cp.Close()
				if err != nil {
					log.InfoLog("close PartitionConsumer failed:", zap.Error(err))
				}
			}()

			for {
				select {
				case msg := <-cp.Messages():
					err := middlewareConsumerHandler(fn)(msg)
					if err != nil {
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(cp)
	}

	return nil
}

func SendMsgToKafka() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := range [10]int{} {
		err := SendMessage(ctx, "broker1", "xiaojiao", fmt.Sprintf("hello world %d", i))
		if err != nil {
			log.ErrorLog("Failed to send message", zap.Error(err))
		}
	}
}

func ConsumeMsgFromKafka() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := Consumer(ctx, "broker1", "xiaojiao", func(msg *sarama.ConsumerMessage) error {
		fmt.Println("Received message: ", string(msg.Value))
		log.InfoLog("consume message", zap.String("msg", string(msg.Value)))
		return nil
	})
	if err != nil {
		log.ErrorLog("Failed to start consumer", zap.Error(err))
	}

	select {}
}

func KafkaTest() {
	SendMsgToKafka()
	time.Sleep(time.Second * 1)
	ConsumeMsgFromKafka()
}
