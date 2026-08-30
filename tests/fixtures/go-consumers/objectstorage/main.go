package main

import (
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage/s3store"
)

func main() {
	var _ objectstorage.Reader = (*s3store.Client)(nil)
	_ = objectstorage.ErrNotFound
}
