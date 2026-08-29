package main

import (
	sharedapi "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/api"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage/s3store"
)

func main() {
	_ = sharedapi.Envelope{}
	var _ objectstorage.Reader = (*s3store.Client)(nil)
}
