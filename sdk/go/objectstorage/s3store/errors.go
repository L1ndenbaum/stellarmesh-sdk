package s3store

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func wrapProviderError(operation, key string, err error) error {
	if err == nil {
		return nil
	}
	return &objectstorage.Error{Kind: classifyError(err), Operation: operation, Key: key, Err: err}
}

func classifyError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return objectstorage.ErrUnavailable
	}
	var responseError *smithyhttp.ResponseError
	if errors.As(err, &responseError) {
		switch responseError.HTTPStatusCode() {
		case http.StatusBadRequest:
			return objectstorage.ErrInvalidArgument
		case http.StatusUnauthorized, http.StatusForbidden:
			return objectstorage.ErrForbidden
		case http.StatusNotFound:
			return objectstorage.ErrNotFound
		case http.StatusConflict:
			return objectstorage.ErrConflict
		case http.StatusPreconditionFailed:
			return objectstorage.ErrPreconditionFailed
		}
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch strings.ToLower(apiError.ErrorCode()) {
		case "nosuchkey", "nosuchbucket", "notfound", "nosuchupload", "invalidobjectstate":
			return objectstorage.ErrNotFound
		case "accessdenied", "invalidaccesskeyid", "signaturedoesnotmatch", "expiredtoken", "tokenrefreshrequired":
			return objectstorage.ErrForbidden
		case "preconditionfailed", "conditionalrequestconflict":
			return objectstorage.ErrPreconditionFailed
		case "bucketalreadyexists", "bucketalreadyownedbyyou", "operationaborted", "conflict":
			return objectstorage.ErrConflict
		case "invalidargument", "invalidrequest", "invalidpart", "invalidpartorder", "entitytoolarge", "entitytoosmall":
			return objectstorage.ErrInvalidArgument
		}
	}
	return objectstorage.ErrUnavailable
}

func errorKind(err error) error {
	for _, kind := range []error{
		objectstorage.ErrNotFound,
		objectstorage.ErrForbidden,
		objectstorage.ErrPreconditionFailed,
		objectstorage.ErrConflict,
		objectstorage.ErrInvalidArgument,
		objectstorage.ErrUnavailable,
	} {
		if errors.Is(err, kind) {
			return kind
		}
	}
	return objectstorage.ErrUnavailable
}
