package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	jobsv1 "jobmaster/api/gen/proto/go/jobs/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// ClientConfig hold configuration information for the client.
type ClientConfig struct {
	// ClientName is the name of the client/user who will be calling RPCs on the server.
	ClientName string

	// ClientCertPathPrefix is the path prefix under which the client's certificate key file
	// and certificate file may be found.
	ClientCertPathPrefix string

	// ServerName is the name/IP of the gRPC server to which the client will connect.
	ServerName string

	// ServerPort is the port on the gRPC server to which the client connection will be made.
	ServerPort string

	// ServerCACertFile is the path to the CA cert file for the CA who signed the server's TLS certificate.
	ServerCACertFile string
}

// NewClient creates a new jobmaster gRPC client.
func NewClient(cc ClientConfig) (jobsv1.JobServiceClient, *grpc.ClientConn, error) {
	// Sanity
	if cc.ClientName == "" {
		return nil, nil, errors.New("ClientName not provided in config")
	}

	if cc.ClientCertPathPrefix == "" {
		return nil, nil, errors.New("ClientCertPathPrefix not provided in config")
	}

	if cc.ServerName == "" {
		return nil, nil, errors.New("ServerName not provided in config")
	}

	if cc.ServerPort == "" {
		return nil, nil, errors.New("ServerPort not provided in config")
	}

	if cc.ServerCACertFile == "" {
		return nil, nil, errors.New("ServerCACertFile not provided in config")
	}

	dialString := fmt.Sprintf("%s:%s", cc.ServerName, cc.ServerPort)

	tlsConfig, err := loadTLSConfigForClient(cc)
	if err != nil {
		return nil, nil, err
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(tlsConfig),
	}

	conn, err := grpc.NewClient(dialString, opts...)
	if err != nil {
		return nil, nil, err
	}

	rpcClient := jobsv1.NewJobServiceClient(conn)
	return rpcClient, conn, nil
}

// loadTLSConfig creates the TLS transport credentials for the client.
func loadTLSConfigForClient(cc ClientConfig) (credentials.TransportCredentials, error) {
	// TODO: In production, get certificate paths and names from a secrets repo
	certName := fmt.Sprintf("%s_cert.pem", cc.ClientName)
	keyName := fmt.Sprintf("%s_key.pem", cc.ClientName)
	certFile := filepath.Join(cc.ClientCertPathPrefix, certName)
	keyFile := filepath.Join(cc.ClientCertPathPrefix, keyName)

	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed loading server certificate: %w", err)
	}

	// The CA which we trust with issuing the server's TLS cert.
	srvCAData, err := os.ReadFile(cc.ServerCACertFile)
	if err != nil {
		return nil, fmt.Errorf("failed reading CA certificate: %w", err)
	}

	srvCAPool := x509.NewCertPool()
	if !srvCAPool.AppendCertsFromPEM(srvCAData) {
		return nil, errors.New("unable to append certificates to the CA pool")
	}

	// Note: TLSv1.3 cipher suites are not configurable as of Go 1.22.4.
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		RootCAs:      srvCAPool,
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}

	return credentials.NewTLS(tlsConfig), nil
}
