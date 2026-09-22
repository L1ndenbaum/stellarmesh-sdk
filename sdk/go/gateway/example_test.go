package gateway_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
)

func ExampleNew() {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	handler, err := gateway.New(
		gateway.WithRoutes(gateway.Route{Name: "public", Match: gateway.RouteMatch{ExactPath: "/items"}, Upstream: "api", Access: gateway.AccessPublic}),
		gateway.WithUpstreams(gateway.Upstream{Name: "api", URL: upstream.URL}),
		gateway.WithoutAccessLog(),
	)
	if err != nil {
		panic(err)
	}
	proxy := httptest.NewServer(handler)
	defer proxy.Close()
	response, err := proxy.Client().Get(proxy.URL + "/items")
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		panic(err)
	}
	fmt.Println(response.StatusCode)
	// Output: 204
}
