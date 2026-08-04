package dto

// ErrorResponse is the standard error envelope for API responses.
// Success should always be false. Use NewErrorResponse to construct.
type ErrorResponse struct {
	Success bool           `json:"success"`
	Error   string         `json:"error"`
	Code    string         `json:"code,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// NewErrorResponse creates an ErrorResponse with Success set to false.
func NewErrorResponse(err, code string) ErrorResponse {
	return ErrorResponse{
		Success: false,
		Error:   err,
		Code:    code,
	}
}

// ListResponse is a generic typed list envelope for API responses.
// Success should always be true. Use NewListResponse to construct,
// which ensures Data is an empty slice (not nil) to avoid "data":null in JSON.
type ListResponse[T any] struct {
	Success bool `json:"success"`
	Data    []T  `json:"data"`
	Total   int  `json:"total"`
}

// NewListResponse creates a ListResponse with Success set to true.
// If items is nil, Data is initialized to an empty slice to ensure
// JSON serialization produces "data":[] instead of "data":null.
func NewListResponse[T any](items []T, total int) ListResponse[T] {
	if items == nil {
		items = []T{}
	}
	return ListResponse[T]{
		Success: true,
		Data:    items,
		Total:   total,
	}
}

// MessageResponse is a simple acknowledgment envelope for API responses.
// Success should always be true. Use NewMessageResponse to construct.
type MessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// NewMessageResponse creates a MessageResponse with Success set to true.
func NewMessageResponse(msg string) MessageResponse {
	return MessageResponse{
		Success: true,
		Message: msg,
	}
}
