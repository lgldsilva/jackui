package httpshared

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// CodeField is the stable machine-readable error-code member of the JSON
// error envelope (issue #201): {"error": "...", "code": "..."}. Native
// clients branch on `code` — never on the prose or on stringified statuses.
const CodeField = "code"

// ErrorCodeForStatus maps an HTTP status onto the stable slug carried in
// every error response. Handlers may override it per-response by passing
// CodeField inside the optional fields (e.g. "invalid_device_key"); the
// status-derived default keeps the contract total without touching every
// call site.
func ErrorCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusGone:
		return "gone"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case StatusClientClosedRequest:
		return "client_closed_request"
	case http.StatusBadGateway:
		return "bad_gateway"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusGatewayTimeout:
		return "gateway_timeout"
	}
	if status >= 500 {
		return "internal_error"
	}
	return "error"
}

// errorFieldOverride reads an explicit CodeField override out of the optional
// fields, returning "" when absent.
func errorCodeOverride(fields gin.H) string {
	if fields == nil {
		return ""
	}
	code, _ := fields[CodeField].(string)
	return code
}

// RespondError writes the standard JSON error response used by HTTP handlers.
func RespondError(c *gin.Context, status int, err error) {
	RespondErrorFields(c, status, err, nil)
}

// RespondErrorMessage is RespondError's string-valued counterpart for
// validation and other errors that are not represented by an error value.
func RespondErrorMessage(c *gin.Context, status int, message string) {
	RespondErrorMessageFields(c, status, message, nil)
}

// RespondErrorFields preserves the standard error member while adding the
// optional fields used by a few machine-readable error responses.
func RespondErrorFields(c *gin.Context, status int, err error, fields gin.H) {
	RespondErrorMessageFields(c, status, err.Error(), fields)
}

// RespondErrorMessageFields is RespondErrorMessage with optional fields.
// Every response carries CodeField: an explicit fields override or the
// status-derived default (see ErrorCodeForStatus).
func RespondErrorMessageFields(c *gin.Context, status int, message string, fields gin.H) {
	code := errorCodeOverride(fields)
	if code == "" {
		code = ErrorCodeForStatus(status)
	}
	body := gin.H{ErrorField: message, CodeField: code}
	for key, value := range fields {
		body[key] = value
	}
	c.JSON(status, body)
}

// StatusClientClosedRequest is the de-facto (nginx) status for a request the
// client abandoned before the response was written.
const StatusClientClosedRequest = 499

// AbortIfClientGone reports whether err happened because the request's own
// context ended (client disconnect). It then aborts with a bodiless 499 so a
// cancelled database call is neither logged as a failure nor leaked to the
// client as a 500 carrying the driver's message. Callers return when it is true.
func AbortIfClientGone(c *gin.Context, err error) bool {
	if err == nil || c.Request.Context().Err() == nil {
		return false
	}
	c.AbortWithStatus(StatusClientClosedRequest)
	return true
}

// DetachedWriteTimeout bounds a write that must survive its request.
const DetachedWriteTimeout = 10 * time.Second

// DetachedCtx returns a context for a small DB write that must complete even
// if the client has gone (search results cached after an aborted SSE, resume
// position on a hard tab close, incognito cleanup on logout). It keeps the
// parent's values but not its cancellation, and is bounded by
// DetachedWriteTimeout so a hung Postgres cannot leak the caller.
func DetachedCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), DetachedWriteTimeout)
}
