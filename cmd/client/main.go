package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"jobmaster/api/client"
)

// Some variables to hold the results of parsing the command line.
var (
	// clientName is the name of the "user"/client who is making requests to the server.
	// In addition to the mTLS client certificate, the server will perform a simple
	// OAuth2 token string identification against this name.
	// In production, this would either be a system user (PAM, etc) or a user from
	// a user management system which stores usernames, hashed passwords, etc.
	clientName string

	// The operation to perform. This is one of the following: start, stop, status, statusall, streamoutput.
	operation string

	// Arguments to the operation. additionalArgs will be used for the "start" operation
	opArg          string
	additionalArgs []string
)

// Consts for default values for some required parameters.
// TODO: Get these from a config, or additional command line args
const (
	serverName        = "localhost"
	serverPort        = "6443"
	certPathPrefixRel = "../../certs"
	serverCACertFile  = "server_ca_cert.pem"
)

func usage(myName string) {
	usageStr := fmt.Sprintf(`Usage: %s -n CLIENT_NAME OPERATION [OP_ARGS...]
A gRPC client which executes arbitrary Linux commands on the jobmaster gRPC server.

Arguments:
  -n, --name CLIENT_NAME
      MANDATORY. The name of user/client with which you wish to invoke the RPCs.
	  -n/--name and CLIENT_NAME MUST be first two arguments to the program.

  OPERATION
    MANDATORY. The operation corresponding to the RPC call to make. This is one of:
      start           start/execute a command on the RPC server.

	  stop            stop a running command on the server.

	  status          show the status of a command previously executed on the server. 

	  streamoutput    stream the output of a job whch was previously started. If the
   	                  job is still running, the RPC call will wait until the server indicates
		              that the job has finished running and that there will be no more output.
					  If the job has completed running (or was terminated), the call will finish
			          after streaming all available output from the stopped/terminated job.

  OP_ARGS
    The arguments to OPERATION. The JOB_ID is case-sensitive.

	  OPERATION start
	    COMMAND [ARGS]  The COMMAND to run on the server, along with ARGS required for it.

	  OPERATION stop
	    JOB_ID          The job ID of a job which was previously started.

	  OPERATION status
	    JOB_ID          The job ID of a job which was previously started.

	  OPERATION streamoutput
	    JOB_ID          The job ID of a job which was previously started.

Note that the name and port of the server to connect to, are not currently configurable.
They will be made configurable in a future release.

Examples:
  %s --name client1 start dd if=/dev/urandom of=/dev/null bs=1M count=100
  %s -n client1 start ls
  %s -n client1 stop <JOBID>
  %s -n client1 status <JOBID>
  %s -n client1 streamoutput <JOBID>
`, myName, myName, myName, myName, myName, myName) // Whoo! Quite the narcissist, eh :-)

	fmt.Println(usageStr)
}

func parseCommandLineArgs(cargs []string) error {
	cLen := len(cargs)
	if cLen < 3 {
		return errors.New("incorrect number of args specified")
	}

	if (cargs[0] != "-n") && (cargs[0] != "--name") {
		return errors.New("-n/--name CLIENT_NAME is required/mandatory and MUST be the first argument to the program")
	}
	clientName = cargs[1]
	opAndArgs := cargs[2:]
	oaLen := len(opAndArgs)

	if clientName == "" {
		return errors.New("-n/--name CLIENT_NAME is empty - it is required/mandatory and MUST be the first argument to the program")
	}

	operation = strings.ToLower(opAndArgs[0])
	switch operation {
	case "start", "stop", "status", "streamoutput":
		if oaLen < 2 {
			return fmt.Errorf("operation %q requires an argument", operation)
		}

		opArg = opAndArgs[1]
		if oaLen >= 3 {
			additionalArgs = opAndArgs[2:]
		}

		return nil

	default:
		return fmt.Errorf("unknown operation %q", operation)
	}
}

func main() {
	myName, err := os.Executable()
	if err != nil {
		myName = "client"
	}
	myName = filepath.Base(myName)

	certPathPrefixAbs, err := filepath.Abs(certPathPrefixRel)
	if err != nil {
		log.Fatalf("Failed to get absolute path to certificates dir: %v", err)
	}

	// Command line arg parsing. We roll our own because args to the start command cause
	// standard command line flag parsers to barf if the list contains a hyphened arg,
	// for example having the start command be "ls -l /etc". Fun, eh?
	err = parseCommandLineArgs(os.Args[1:])
	if err != nil {
		usage(myName)
		log.Printf("failed parsing command line arguments: %v", err)
		return
	}

	// Set up a termination context.
	// Standard Linux termination signals; see https://www.gnu.org/software/libc/manual/html_node/Termination-Signals.html
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		syscall.SIGHUP,
	)
	defer cancel()

	cc := client.ClientConfig{
		ClientName:           clientName,
		ServerName:           serverName,
		ServerPort:           serverPort,
		ClientCertPathPrefix: certPathPrefixAbs,
		ServerCACertFile:     filepath.Join(certPathPrefixAbs, serverCACertFile),
	}

	rpcClient, conn, err := client.NewClient(cc)
	if err != nil {
		log.Printf("error creating RPC client: %v", err)
		return
	}

	defer conn.Close()

	var rpcErr error

	switch operation {
	case "start":
		rpcErr = client.Start(ctx, rpcClient, opArg, additionalArgs)

	case "stop":
		rpcErr = client.Stop(ctx, rpcClient, opArg)

	case "status":
		rpcErr = client.Status(ctx, rpcClient, opArg)

	case "streamoutput":
		rpcErr = client.StreamOutput(ctx, rpcClient, opArg)

	default:
		// Shouldn't happen, but it doesn't hurt to be paranoid.
		usage(myName)
		log.Printf("%q is not a valid operation", operation)
		return
	}

	if rpcErr != nil {
		log.Printf("ERROR from RPC: %v", rpcErr)
	}
}
