package gmail

import (
	"errors"
	"net/http"

	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func mapError(err error) error {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case http.StatusUnauthorized:
			return status.Errorf(codes.Unauthenticated, "gmail: %s", apiErr.Message)
		case http.StatusForbidden:
			return status.Errorf(codes.PermissionDenied, "gmail: %s", apiErr.Message)
		case http.StatusNotFound:
			return status.Errorf(codes.NotFound, "gmail: %s", apiErr.Message)
		case http.StatusTooManyRequests:
			return status.Errorf(codes.ResourceExhausted, "gmail: %s", apiErr.Message)
		}
		if apiErr.Code >= 500 {
			return status.Errorf(codes.Unavailable, "gmail: %s", apiErr.Message)
		}
		return status.Errorf(codes.Internal, "gmail: %s", apiErr.Message)
	}
	return status.Errorf(codes.Internal, "gmail: %v", err)
}
