// Package httpkit is the shared HTTP transport of the REST API and the probe servers:
// request correlation and recovery middleware, strict JSON and query-string parsing,
// and RFC 9457 problem responses. It holds no business rules and does not know any
// individual endpoint.
package httpkit
