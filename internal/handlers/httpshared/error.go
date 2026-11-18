package httpshared

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

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
func RespondErrorMessageFields(c *gin.Context, status int, message string, fields gin.H) {
	body := gin.H{ErrorField: message}
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
