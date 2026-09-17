package gateway

type contextKey uint8

const (
	requestIDContextKey contextKey = iota
	routeContextKey
	identityContextKey
	clientIPContextKey
	accessLogStateContextKey
)
