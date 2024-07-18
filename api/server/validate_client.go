package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"jobmaster/api/authn"
	"jobmaster/api/authz"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Constants with the string keys used to get client info from the context.
// TODO: Get these from a config file
type ctxKey string // To make staticcheck happy
const (
	clientNameCtxKey ctxKey = "jobmaster-clientname"
	clientRoleCtxKey ctxKey = "jobmaster-clientrole"
)

// NOTE: As of grpc-go v1.64.0, a gRPC stream interceptor unaccountably provides no easy way
// to manipulate its context. Therefore, we go though shenanigans to be able to add client/user
// information to the context of a streaming call (which will be used in the actual RPC handlers).
// The stream-wrapping code below was inspired by:
//    https://github.com/grpc/grpc-go/blob/v1.64.0/examples/features/interceptor/server/main.go#L106-L124
//    https://github.com/grpc-ecosystem/go-grpc-middleware/blob/v2.1.0/wrappers.go

// wrappedStream wraps the original server stream along with a new custom context.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

// Context returns the custom context in the wrappedStream.
func (w *wrappedStream) Context() context.Context {
	return w.ctx
}

// newWrappedStream creates a wrappedStream from the original ServerStream and the given
// custom context.
func newWrappedStream(ctx context.Context, ss grpc.ServerStream) *wrappedStream {
	return &wrappedStream{
		ss,
		ctx,
	}
}

// validateClientStreamInterceptor is an interceptor for server-side streaming RPCs.
func validateClientStreamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {

	newctx, err := validateClient(ss.Context())
	if err != nil {
		log.Printf("streaming RPC interceptor error: %v", err)
		return err
	}

	// Shenanigans
	err = handler(srv, newWrappedStream(newctx, ss))
	if err != nil {
		log.Printf("streaming RPC interceptor error: %v", err)
	}

	return err
}

// validateClientUnaryInterceptor is an interceptor for server-side unary RPCs, which validates
// the client connection. It creates a new context from the existing context for the handler,
// with two additional key-value pairs in the context: the validated client name, and a
// string representation of the validated client's authz role. These new context items will
// be used in servicing the gRPC requests made to the server.
func validateClientUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {

	newctx, err := validateClient(ctx)
	if err != nil {
		log.Printf("unary RPC interceptor error: %v", err)
		return nil, err
	}

	return handler(newctx, req)
}

// ValidateClientFromTLSInfo validates the client's TLS certificate data.
// This is a simple check which sees whether the certificate has an
// allowed crypto scheme and whether the certificate subject contains
// a client from the list of allowed client/user names.
func ValidateClientFromTLSInfo(mtls credentials.TLSInfo) (string, authz.Role, error) {
	clientName := ""
	clientRole := authz.Unauthorized
	var err error
	for _, item := range mtls.State.PeerCertificates {
		// Validate the X509 certificate's crypto and validity period.
		err = authn.ValidateCertCryptoAndValidity(item)
		if err != nil {
			return "", authz.Unauthorized, status.Errorf(codes.Unauthenticated,
				"client certificate validity check error: %s", err.Error())
		}

		// Now get the client name from the CN attribute of the cert
		clientName, err = authn.GetJobmasterEntityNameFromCertCN(item)
		if err != nil {
			return "", authz.Unauthorized, status.Errorf(codes.Unauthenticated,
				"client certificate parse error: %s", err.Error())
		}

		// Check whether the client is authorized.
		_, clientRole = authz.IsClientAuthorized(clientName)
		if clientRole == authz.Unauthorized {
			return "", authz.Unauthorized, status.Error(codes.Unauthenticated,
				fmt.Sprintf("%s is not authorized", clientName))
		}

	}

	return clientName, clientRole, nil
}

// validateClient validates the client credentials contained in ctx.
// If the client is validated, it returns a new context created from the existing context,
// with two additional key-value pairs in the context:
// the client name as the value for the new "jobmaster-clientname" key in the context, and a
// string representation of the client's role as the value for the new "jobmaster-clientrole"
// key in the new context.
func validateClient(ctx context.Context) (context.Context, error) {
	// TODO: In production, beef up the client checks, for example by checking
	// a client/user password hash against a database or other repository of
	// user/auth data. For other possible checks against the certificate and
	// token, see the appropriate validation functions above.

	select {
	// First, check whether we even need to do anything
	case <-ctx.Done():
		return ctx, status.Error(codes.Canceled, "interceptor: context was canceled")

	default:
		clientName := ""
		clientRole := authz.Unauthorized
		var err error

		// Validate the client from the TLS certificate data.
		if p, ok := peer.FromContext(ctx); ok {
			if mtls, ok := p.AuthInfo.(credentials.TLSInfo); ok {
				clientName, clientRole, err = ValidateClientFromTLSInfo(mtls)
				if err != nil {
					return nil, err
				}
			}
		}

		// Paranoia, since we're using the comma-ok idiom above
		if clientName == "" || clientRole == authz.Unauthorized {
			return nil, status.Error(codes.Unauthenticated,
				"did not find an authorized client")
		}

		// If we got here, the client is authorized.
		clientRoleStr := string(clientRole)
		log.Printf("Client Certificate is from allowed client %s with role %s", clientName, clientRoleStr)

		// Create a new context from the old one, with new keys containing the client name and
		// client role (as string).
		newctx := context.WithValue(ctx, clientNameCtxKey, clientName)
		newctx = context.WithValue(newctx, clientRoleCtxKey, clientRoleStr)

		return newctx, nil
	}
}

// ClientInfoFromContext fetches the client info from the provided context and validates it.
func ClientInfoFromContext(ctx context.Context) (clientName, clientRoleStr string, err error) {
	clientName = ctx.Value(clientNameCtxKey).(string)
	if clientName == "" || strings.TrimSpace(clientName) == "" {
		return "", "", errors.New("client name in context is empty")
	}

	isAuth, cRole := authz.IsClientAuthorized(clientName)
	if !isAuth {
		return "", "", fmt.Errorf("client %q is not authorized", clientName)
	}

	clientRoleStr = ctx.Value(clientRoleCtxKey).(string)
	if clientRoleStr == "" || strings.TrimSpace(clientRoleStr) == "" {
		return "", "", errors.New("client role in context is empty")
	}

	clientRole := authz.RoleStrToRole(clientRoleStr)
	if clientRole == authz.Unauthorized {
		return "", "", fmt.Errorf("client %q context role is 'Unauthorized'", clientName)
	}

	if clientRole != cRole {
		return "", "", errors.New("client role mismatch")
	}

	return clientName, clientRoleStr, nil
}
