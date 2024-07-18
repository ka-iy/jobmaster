package main

import (
	"context"
	"log"
	"os/signal"
	"path/filepath"
	"syscall"

	"jobmaster/api/server"

	"github.com/jessevdk/go-flags"
)

// For command-line args. TODO: Get these from a config file
var cmdflags struct {
	JobOutputDir string `short:"o" long:"output-dir" description:"The absolute path to the directory where job output files will be written" default:"/tmp"`
	IODevID      string `short:"d" long:"iodevice-id" description:"The Linux device ID (<MAJOR>:<MINOR>) for the IO device to set cgroup limits" required:"true"`
}

// For the certificate paths. TODO: Get these from a config too
const (
	certsDirRelative  = "../../certs"
	serverCertName    = "server1_cert.pem"
	serverCertKeyName = "server1_key.pem"
	clientCACertName  = "client_ca_cert.pem"
)

// The port on which this server will run. TODO: Get from a config file
const serverPort = "6443"

func main() {
	_, _ = flags.Parse(&cmdflags)

	if cmdflags.IODevID == "" {
		log.Fatal("Require IO device identifier in <MAJ>:<MIN> format")
	}

	certsDir, err := filepath.Abs(certsDirRelative)
	if err != nil {
		log.Fatalf("Failed to get absolute path to certs dir: %v", err)
	}

	log.Printf("Certificate directory: %s", certsDir)

	sc := server.ServerConfig{
		ServerPort:        serverPort,
		ServerCertKeyFile: filepath.Join(certsDir, serverCertKeyName),
		ServerCertFile:    filepath.Join(certsDir, serverCertName),
		ClientCAFile:      filepath.Join(certsDir, clientCACertName),
		JobOutputDirPath:  cmdflags.JobOutputDir,
		IODeviceID:        cmdflags.IODevID,
	}

	// Standard Linux termination signals. See https://www.gnu.org/software/libc/manual/html_node/Termination-Signals.html
	termctx, endIt := signal.NotifyContext(context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		syscall.SIGHUP,
	)
	defer endIt()

	err = server.Run(termctx, sc)

	if err != nil {
		log.Printf("Server ended with error: %v", err)
	}

	log.Println("Exiting...")
}
