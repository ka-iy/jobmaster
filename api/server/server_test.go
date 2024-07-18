package server

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"jobmaster/api/client"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// Paths to test certs/crypto
const (
	goodCertsPath = "../../certs"
	badCertsPath  = "../../certs/BADCERTS-For-Testing"
)

// Config options
const (
	serverPort       = "6443"
	jobOutputDirPath = "/tmp" // Remember to clean up logs from here
)

// Command-line arg for device ID
var ioDevID = flag.String("iodev", "", "Linux block device ID in <MAJ>:<MIN> format to use for setting IO cgroup quota")

// TODO: Add more asserts, or more detailed expects, to check the content of the errors.
// TODO: More tests

func TestHarness(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatal("Please run this test as the root user on a Linux system.")
	}

	defer goleak.VerifyNone(t)

	require.NotEmpty(t, *ioDevID, "Require Linux block device ID in <MAJ>:<MIN> format")

	// Get the current path to this test file. We need it for building up
	// the certs path especially if the test is run from another directory
	_, filename, _, _ := runtime.Caller(0)
	require.NotContains(t, filename, "github.com")
	myDir := filepath.Dir(filename)
	t.Logf("Test directory......: %s", myDir)
	testCertPathGood := filepath.Join(myDir, goodCertsPath)
	t.Logf("Good certs directory: %s", testCertPathGood)
	testCertPathBad := filepath.Join(myDir, badCertsPath)
	t.Logf("Bad certs directory : %s", testCertPathBad)

	// testTable is the table containing the configurations for the various tests.
	testTable := []struct {
		name                        string
		sc                          ServerConfig
		cc                          client.ClientConfig
		expectServerError           bool
		expectedServerErrorContains string
		expectClientError           bool
		expectedClientErrorContains string
		expectRPCError              bool
		expectedRPCErrorContains    string
	}{
		{
			name: "HappyPath",
			sc: ServerConfig{
				ServerPort:        serverPort,
				ServerCertKeyFile: filepath.Join(testCertPathGood, "server1_key.pem"),
				ServerCertFile:    filepath.Join(testCertPathGood, "server1_cert.pem"),
				ClientCAFile:      filepath.Join(testCertPathGood, "client_ca_cert.pem"),
				JobOutputDirPath:  jobOutputDirPath,
				IODeviceID:        *ioDevID,
			},
			cc: client.ClientConfig{
				ClientName:           "client1",
				ServerName:           "localhost",
				ServerPort:           serverPort,
				ClientCertPathPrefix: testCertPathGood,
				ServerCACertFile:     filepath.Join(testCertPathGood, "server_ca_cert.pem"),
			},
		},
		{
			name: "UnknownClient",
			sc: ServerConfig{
				ServerPort:        serverPort,
				ServerCertKeyFile: filepath.Join(testCertPathBad, "unknownclient", "server1_key.pem"),
				ServerCertFile:    filepath.Join(testCertPathBad, "unknownclient", "server1_cert.pem"),
				ClientCAFile:      filepath.Join(testCertPathBad, "unknownclient", "client_ca_cert.pem"),
				JobOutputDirPath:  jobOutputDirPath,
				IODeviceID:        *ioDevID,
			},
			cc: client.ClientConfig{
				ClientName:           "client666",
				ServerName:           "localhost",
				ServerPort:           serverPort,
				ClientCertPathPrefix: filepath.Join(testCertPathBad, "unknownclient"),
				ServerCACertFile:     filepath.Join(testCertPathBad, "unknownclient", "server_ca_cert.pem"),
			},
			expectRPCError:           true,
			expectedRPCErrorContains: "not authorized",
		},
		{
			name: "ExpiredClientCert",
			sc: ServerConfig{
				ServerPort:        serverPort,
				ServerCertKeyFile: filepath.Join(testCertPathGood, "server1_key.pem"),
				ServerCertFile:    filepath.Join(testCertPathGood, "server1_cert.pem"),
				ClientCAFile:      filepath.Join(testCertPathBad, "expired", "expired_ca_cert.pem"),
				JobOutputDirPath:  jobOutputDirPath,
				IODeviceID:        *ioDevID,
			},
			cc: client.ClientConfig{
				ClientName:           "client1",
				ServerName:           "localhost",
				ServerPort:           serverPort,
				ClientCertPathPrefix: filepath.Join(testCertPathBad, "expired"),
				ServerCACertFile:     filepath.Join(testCertPathGood, "server_ca_cert.pem"),
			},
			expectRPCError:           true,
			expectedRPCErrorContains: "expired",
		},
		{
			name: "ExpiredServerCert",
			sc: ServerConfig{
				ServerPort:        serverPort,
				ServerCertKeyFile: filepath.Join(testCertPathBad, "expired", "server1_key.pem"),
				ServerCertFile:    filepath.Join(testCertPathBad, "expired", "server1_cert.pem"),
				ClientCAFile:      filepath.Join(testCertPathGood, "client_ca_cert.pem"),
				JobOutputDirPath:  jobOutputDirPath,
				IODeviceID:        *ioDevID,
			},
			cc: client.ClientConfig{
				ClientName:           "client1",
				ServerName:           "localhost",
				ServerPort:           serverPort,
				ClientCertPathPrefix: testCertPathGood,
				ServerCACertFile:     filepath.Join(testCertPathBad, "expired", "expired_ca_cert.pem"),
			},
			expectRPCError:           true,
			expectedRPCErrorContains: "expired",
		},
		{
			name: "WeakCryptoClientCert",
			sc: ServerConfig{
				ServerPort:        serverPort,
				ServerCertKeyFile: filepath.Join(testCertPathGood, "server1_key.pem"),
				ServerCertFile:    filepath.Join(testCertPathGood, "server1_cert.pem"),
				ClientCAFile:      filepath.Join(testCertPathBad, "weakec", "client_ca_cert.pem"),
				JobOutputDirPath:  jobOutputDirPath,
				IODeviceID:        *ioDevID,
			},
			cc: client.ClientConfig{
				ClientName:           "client1",
				ServerName:           "localhost",
				ServerPort:           serverPort,
				ClientCertPathPrefix: filepath.Join(testCertPathBad, "weakec"),
				ServerCACertFile:     filepath.Join(testCertPathGood, "server_ca_cert.pem"),
			},
			expectClientError:           true,
			expectedClientErrorContains: "unsupported elliptic curve",
		},
		{
			name: "WeakCryptoServerCert",
			sc: ServerConfig{
				ServerPort:        serverPort,
				ServerCertKeyFile: filepath.Join(testCertPathBad, "weakec", "server1_key.pem"),
				ServerCertFile:    filepath.Join(testCertPathBad, "weakec", "server1_cert.pem"),
				ClientCAFile:      filepath.Join(testCertPathGood, "client_ca_cert.pem"),
				JobOutputDirPath:  jobOutputDirPath,
				IODeviceID:        *ioDevID,
			},
			cc: client.ClientConfig{
				ClientName:           "client1",
				ServerName:           "localhost",
				ServerPort:           serverPort,
				ClientCertPathPrefix: testCertPathGood,
				ServerCACertFile:     filepath.Join(testCertPathBad, "weakec", "server_ca_cert.pem"),
			},
			expectServerError:           true,
			expectedServerErrorContains: "unsupported elliptic curve",
		},
		{
			name: "WeakCryptoDigestClient",
			sc: ServerConfig{
				ServerPort:        serverPort,
				ServerCertKeyFile: filepath.Join(testCertPathGood, "server1_key.pem"),
				ServerCertFile:    filepath.Join(testCertPathGood, "server1_cert.pem"),
				ClientCAFile:      filepath.Join(testCertPathBad, "weaksha", "client_ca_cert.pem"),
				JobOutputDirPath:  jobOutputDirPath,
				IODeviceID:        *ioDevID,
			},
			cc: client.ClientConfig{
				ClientName:           "client1",
				ServerName:           "localhost",
				ServerPort:           serverPort,
				ClientCertPathPrefix: filepath.Join(testCertPathBad, "weaksha"),
				ServerCACertFile:     filepath.Join(testCertPathGood, "server_ca_cert.pem"),
			},
			expectRPCError:           true,
			expectedRPCErrorContains: "tls: unknown",
		},
	} // testTable

	for _, tc := range testTable {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			// Note: "defer cancel()" does not seem to work in tests.
			// We need to explicitly call cancel() at the end to stop the server.

			srvErrChan := make(chan error, 1)
			go func() {
				err := Run(ctx, tc.sc)
				srvErrChan <- err
			}()

			if !tc.expectServerError {
				if tc.expectClientError {
					_, _, err := client.NewClient(tc.cc)
					require.Error(t, err)
					if tc.expectedClientErrorContains != "" {
						require.ErrorContains(t, err, tc.expectedClientErrorContains)
					}
					t.Logf("Testing: Client creation error: %v", err)
				} else {
					rpcClient, conn, err := client.NewClient(tc.cc)
					require.NoError(t, err)
					rpcErr := client.Start(ctx, rpcClient, "ls", []string{"-l", "/etc"})
					if tc.expectRPCError {
						require.Error(t, rpcErr)
						if tc.expectedRPCErrorContains != "" {
							require.ErrorContains(t, rpcErr, tc.expectedRPCErrorContains)
						}

						t.Logf("Testing: Client RPC error: %v", rpcErr)
					}
					conn.Close()
				}
			}

			// Need to explicitly Call cancel() here after eveything is done.
			cancel()
			serr := <-srvErrChan
			if serr != nil {
				t.Logf("Testing: Server returned error: %v", serr)
			}
			close(srvErrChan)
		})
	}
}
