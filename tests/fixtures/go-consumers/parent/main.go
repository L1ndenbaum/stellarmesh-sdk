package main

import (
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/envconfig"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/jsonbody"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/server"
)

func main() {
	_ = envconfig.String
	_ = jsonbody.Options{}
	_ = server.Config{}
}
