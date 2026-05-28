package yahoo

import (
	"net"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func mapError(err error) error {
	if err == nil {
		return nil
	}

	msg := strings.ToUpper(err.Error())

	if strings.Contains(msg, "AUTHENTICATIONFAILED") ||
		strings.Contains(msg, "AUTHENTICATION FAILED") ||
		strings.Contains(msg, "INVALID CREDENTIALS") {
		return status.Errorf(codes.Unauthenticated, "yahoo: %v", err)
	}

	if strings.Contains(msg, "NONEXISTENT") ||
		strings.Contains(msg, "DOES NOT EXIST") ||
		strings.Contains(msg, "NO SUCH") {
		return status.Errorf(codes.NotFound, "yahoo: %v", err)
	}

	var netErr net.Error
	if isNetError(err, &netErr) {
		return status.Errorf(codes.Unavailable, "yahoo: %v", err)
	}

	return status.Errorf(codes.Internal, "yahoo: %v", err)
}

func isNetError(err error, target *net.Error) bool {
	e, ok := err.(net.Error)
	if ok {
		*target = e
	}
	return ok
}
