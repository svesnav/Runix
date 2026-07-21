package httpx

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ErrorBody is the single error shape every endpoint returns.
type ErrorBody struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Error(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, ErrorBody{Error: APIError{Code: code, Message: message}})
}

func BadRequest(c *gin.Context, message string) {
	Error(c, http.StatusBadRequest, "bad_request", message)
}

func Unauthorized(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, "unauthorized", message)
}

func Forbidden(c *gin.Context) {
	Error(c, http.StatusForbidden, "forbidden", "insufficient permissions")
}

func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, "not_found", message)
}

func Conflict(c *gin.Context, message string) {
	Error(c, http.StatusConflict, "conflict", message)
}

func Internal(c *gin.Context) {
	Error(c, http.StatusInternalServerError, "internal", "internal server error")
}

// ServiceError maps well-known service errors onto HTTP statuses so
// handlers stay one-liners. Unknown errors become opaque 500s.
//
// The full error text is returned for recognized errors: services wrap
// their sentinels with the reason ("invalid task: cron: minute: 99 is out
// of range"), and dropping that leaves the caller guessing. Unrecognized
// errors are still hidden, since only those can carry internals.
func ServiceError(c *gin.Context, err error, known map[error]int) {
	for target, status := range known {
		if errors.Is(err, target) {
			Error(c, status, http.StatusText(status), err.Error())
			return
		}
	}
	_ = c.Error(err)
	Internal(c)
}

// Page is normalized pagination input.
type Page struct {
	Page int
	Size int
}

func (p Page) Offset() int { return (p.Page - 1) * p.Size }
func (p Page) Limit() int  { return p.Size }

const (
	defaultPageSize = 25
	maxPageSize     = 200
)

func Pagination(c *gin.Context) Page {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", strconv.Itoa(defaultPageSize)))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	return Page{Page: page, Size: size}
}

// ListResponse is the uniform paginated collection envelope.
type ListResponse[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
	Size  int   `json:"size"`
}

func NewListResponse[T any](items []T, total int64, p Page) ListResponse[T] {
	if items == nil {
		items = []T{}
	}
	return ListResponse[T]{Items: items, Total: total, Page: p.Page, Size: p.Size}
}
