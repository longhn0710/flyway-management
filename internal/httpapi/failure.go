package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"flyway-manager/internal/core"
)

// Only failure auditing outlives a canceled request. Verification, approval
// writes and their success audit remain in the original context/transaction.
func (h phase2) failure(r *http.Request, err error) *core.Error {
	var ce *core.Error
	var auditErr *core.AuditError
	switch {
	case errors.As(err, &auditErr):
		ce = &core.Error{Status: 500, Code: "AUDIT_FAILURE", Message: "Security event could not be recorded."}
	case errors.Is(err, context.DeadlineExceeded):
		ce = &core.Error{Status: 504, Code: "REQUEST_TIMEOUT", Message: "Request did not complete before its deadline."}
	case errors.Is(err, context.Canceled):
		ce = &core.Error{Status: 408, Code: "REQUEST_CANCELLED", Message: "Request was cancelled before it completed."}
	case errors.As(err, &ce):
	default:
		if r.Context().Err() != nil {
			return h.failure(r, r.Context().Err())
		}
		ce = &core.Error{Status: 500, Code: "INTERNAL_ERROR", Message: "An unexpected error occurred."}
		h.logger.Error("API operation failed", "request_id", trace(r).RequestID)
	}
	sensitive := ce.Status == 401 || ce.Status == 403 || ce.Status == 409 ||
		(mutating(r.Method) && ((ce.Status >= 400 && ce.Status < 500) || ce.Code == "REQUEST_TIMEOUT" || ce.Code == "AUDIT_FAILURE"))
	if h.s != nil && sensitive {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
		defer cancel()
		if err := h.s.Event(ctx, trace(r), "request.denied", requestPath(r), "failure"); err != nil {
			h.logger.Error("mandatory security audit failed", "request_id", trace(r).RequestID)
			return &core.Error{Status: 500, Code: "AUDIT_FAILURE", Message: "Security event could not be recorded."}
		}
	}
	return ce
}
