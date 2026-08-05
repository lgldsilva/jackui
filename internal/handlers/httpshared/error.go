package httpshared

import "github.com/gin-gonic/gin"

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
