package main

import (
	"context"

	stellarkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

func main() {
	for _, mechanism := range []stellarkafka.SASLMechanism{
		stellarkafka.SASLMechanismPlain,
		stellarkafka.SASLMechanismSCRAMSHA256,
		stellarkafka.SASLMechanismSCRAMSHA512,
	} {
		connection, err := stellarkafka.NewConnection(stellarkafka.ConnectionConfig{
			SecurityProtocol: stellarkafka.SecurityProtocolSASLPlaintext,
			SASLMechanism:    mechanism,
			Username:         "consumer",
			Password:         "secret",
		})
		if err != nil {
			panic(err)
		}
		_ = connection.Dialer()
		_ = connection.Transport()
	}
	publisher, err := stellarkafka.NewPublisher(stellarkafka.Config{})
	if err != nil {
		panic(err)
	}
	_ = publisher.Publish(context.Background(), nil)
	_ = publisher.Check
	_ = publisher.Close()
	_ = stellarkafka.CheckTopic
	_ = stellarkafka.IsMessageTooLarge
}
