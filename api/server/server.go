package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"os"

	"jobmaster/api/authz"
	jobsv1 "jobmaster/api/gen/proto/go/jobs/v1"
	"jobmaster/jobslib"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// TODO: Rate-limit API calls from a single client.
// This will prevent malicious clients from DDoS'ing the API server.

// ServerConfig contains configuration information required to instantiate the
// jobmaster gRPC server.
type ServerConfig struct {
	// ServerPort is the port on which the server will listen for client requests.
	ServerPort string

	// ServerCertKeyFile is the path to the key file for the server's X.509 certificate.
	ServerCertKeyFile string

	// ServerCertFile is the path to the server's X.509 certificate.
	ServerCertFile string

	// ClientCAFile is the path to the CA certificate used in signing the client's TLS certificates.
	ClientCAFile string

	// JobOutputDirPath is the path to the directory where job output logs will be generated and stored.
	// It is best if this is an absolute path. If this is left empty, the jobmaster jobs library will
	// assign a default directory.
	JobOutputDirPath string

	// ioDeviceID is required for setting up the IO limit/quota for started jobs. It is a Linux block
	// device ID in the form "<MAJOR>:<MINOR>".
	// See https://www.kernel.org/doc/Documentation/admin-guide/devices.txt
	IODeviceID string

	// TODO: Other config options as required
}

// JobServiceServer is the concretization of the job server.
type JobServiceServer struct {
	jobsv1.UnimplementedJobServiceServer

	// JobMgr is the reference to the jobmaster job management library.
	JobMgr *jobslib.JobsLib
}

// NewServer creates a new job service gRPC server with which the clients
// will interact.
func NewServer(sc ServerConfig) (*grpc.Server, error) {
	if sc.IODeviceID == "" {
		return nil, errors.New("config field IODeviceID is empty")
	}

	// Instantiate the jobs library. This also creates the jobmaster cgroup-v2 hierarchy.
	jl, err := jobslib.CreateJobsLib(authz.GetAuthorizedClientNames(), sc.JobOutputDirPath, sc.IODeviceID)
	if err != nil {
		return nil, fmt.Errorf("error instantiating jobmaster jobs library: %w", err)
	}
	if jl == nil {
		return nil, errors.New("job library instantiation returned nil")
	}

	tlsConfig, err := loadTLSConfigForServer(sc)
	if err != nil {
		return nil, fmt.Errorf("error creating server TLS config: %w", err)
	}

	jobServer := new(JobServiceServer)
	jobServer.JobMgr = jl

	opts := []grpc.ServerOption{
		// Middleware/Interceptors
		grpc.UnaryInterceptor(validateClientUnaryInterceptor),
		grpc.StreamInterceptor(validateClientStreamInterceptor),

		// Enable TLS for all incoming connections.
		grpc.Creds(tlsConfig),
	}

	// Instantiate the gRPC server
	server := grpc.NewServer(opts...)
	// Paranoia
	if server == nil {
		return nil, errors.New("gRPC server is nil")
	}

	// Create the service
	jobsv1.RegisterJobServiceServer(server, jobServer)

	return server, nil
}

// loadTLSConfigForServer creates mTLS credentials from the server certificate and
// all available client certificates. A single CA is used for server certificates
// as well as client certificates. See the 'certs' directory at the project root.
func loadTLSConfigForServer(sc ServerConfig) (credentials.TransportCredentials, error) {
	certFile := sc.ServerCertFile
	keyFile := sc.ServerCertKeyFile

	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed loading server certificate: %w", err)
	}

	// Load uo the client's CA
	clientCaFile := sc.ClientCAFile
	ccaData, err := os.ReadFile(clientCaFile)
	if err != nil {
		return nil, fmt.Errorf("failed reading Client CA certificate: %w", err)
	}
	ccapool := x509.NewCertPool()
	if !ccapool.AppendCertsFromPEM(ccaData) {
		return nil, errors.New("unable to append client CA certificate(s) to the client CA pool")
	}

	// Note: TLSv1.3 cipher suites are not configurable as of Go 1.22.4.
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ccapool,
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}
	return credentials.NewTLS(tlsConfig), nil
}

func Run(ctx context.Context, sc ServerConfig) error {
	// Create the service
	s, err := NewServer(sc)
	if err != nil {
		log.Println(err)
		return err
	}

	serverPort := fmt.Sprintf(":%s", sc.ServerPort)

	ec := make(chan error, 1)

	go func() {
		log.Println("Starting jobmaster gRPC server on port", serverPort)

		listener, err := net.Listen("tcp", serverPort)
		if err != nil {
			ec <- err
			return
		}

		err = s.Serve(listener)
		if err != nil {
			// We need to close the listener ourselves
			_ = listener.Close()
		}
		ec <- err
	}()

	select {
	case <-ctx.Done():
		log.Println("Calling context indicates done")
	case err = <-ec:
		if err != nil {
			log.Printf("gRPC server startup returned error: %v", err)
		}
	}

	log.Println("Server is stopping")
	s.GracefulStop()

	// Clean up cgroups
	err = jobslib.TeardownJobmasterCgroups(authz.GetAuthorizedClientNames())
	if err != nil {
		log.Printf("ERROR removing cgroups: %v", err)
	}

	return err
}
