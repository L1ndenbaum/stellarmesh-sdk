package jsonbody_test

import (
	"fmt"
	"net/http/httptest"
	"strings"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/jsonbody"
)

func ExampleDecode() {
	request := httptest.NewRequest("POST", "/items", strings.NewReader(`{"name":"示例"}`))
	response := httptest.NewRecorder()
	var input struct {
		Name string `json:"name"`
	}
	if err := jsonbody.Decode(response, request, &input, jsonbody.Options{DisallowUnknownFields: true}); err != nil {
		fmt.Println("请求无效", err)
		return
	}
	// Decode 已关闭请求体，响应协议由项目决定。
	fmt.Println(input.Name)
	// Output: 示例
}
