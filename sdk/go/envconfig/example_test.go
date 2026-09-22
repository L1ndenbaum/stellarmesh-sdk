package envconfig_test

import (
	"fmt"
	"os"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/envconfig"
)

func ExampleNewStrictLoader() {
	const key = "STELLARMESH_DOC_EXAMPLE_TIMEOUT"
	previous, existed := os.LookupEnv(key)
	if err := os.Setenv(key, "2s"); err != nil {
		panic(err)
	}
	defer func() {
		var err error
		if existed {
			err = os.Setenv(key, previous)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			panic(err)
		}
	}()
	loader := envconfig.NewStrictLoader()
	timeout := loader.Duration(key, time.Second)
	if err := loader.Err(); err != nil {
		panic(err)
	}
	fmt.Println(timeout)
	// Output: 2s
}
