package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	jobsv1 "jobmaster/api/gen/proto/go/jobs/v1"
)

// This file contains the implementations of the RPC calls to be made to the server
// based on the command line args reeived by the client.
// Messages from the server will be printed as-is, without a log timestamp or other preamble.

// Start calls the Start gRPC server endpoint to start the given command with the furnished cmdArgs.
func Start(ctx context.Context, rpcClient jobsv1.JobServiceClient, command string, cmdArgs []string) error {
	if command == "" {
		return errors.New("no command given for Start RPC")
	}

	in := &jobsv1.JobCreateRequest{
		Command: command,
		Args:    cmdArgs,
	}

	resp, err := rpcClient.Start(ctx, in)
	if err != nil {
		return err
	}

	fmt.Println(resp.GetJobID())

	return nil
}

// Stop calls the Stop gRPC endpoint on the gRPC server.
func Stop(ctx context.Context, rpcClient jobsv1.JobServiceClient, jobID string) error {
	if jobID == "" {
		return errors.New("no jobID given for Stop RPC")
	}

	jid := &jobsv1.JobID{JobID: jobID}
	in := &jobsv1.JobIDRequest{JobIDRequest: jid}

	resp, err := rpcClient.Stop(ctx, in)
	if err != nil {
		return err
	}

	fmt.Printf("id=%s,jobMsg=%s,client=%s,pid=%d,state=%d,exitCode=%d,signalstr=%s,cmd=%v,started=%v,ended=%v\n",
		resp.GetJobIDResponse().GetJobID(), resp.GetJobMsg(), resp.GetClient(), resp.GetPid(), resp.GetState(), resp.GetExitCode(),
		resp.GetSignalStr(), resp.GetCmd(), resp.GetStarted().AsTime(), resp.GetEnded().AsTime(),
	)

	return nil
}

// Status calls the Status gRPC endpoint on the gRPC server.
func Status(ctx context.Context, rpcClient jobsv1.JobServiceClient, jobID string) error {
	if jobID == "" {
		return errors.New("no jobID given for Status RPC")
	}

	jid := &jobsv1.JobID{JobID: jobID}
	in := &jobsv1.JobIDRequest{JobIDRequest: jid}

	resp, err := rpcClient.Status(ctx, in)
	if err != nil {
		return err
	}

	fmt.Printf("id=%s,jobMsg=%s,client=%s,pid=%d,state=%d,exitCode=%d,signalstr=%s,cmd=%v,started=%v,ended=%v\n",
		resp.GetJobIDResponse().GetJobID(), resp.GetJobMsg(), resp.GetClient(), resp.GetPid(), resp.GetState(), resp.GetExitCode(),
		resp.GetSignalStr(), resp.GetCmd(), resp.GetStarted().AsTime(), resp.GetEnded().AsTime(),
	)

	return nil
}

// StreamOutput calls the StreamOutput gRPC endpoint on the server.
func StreamOutput(ctx context.Context, rpcClient jobsv1.JobServiceClient, jobID string) error {
	if jobID == "" {
		return errors.New("no jobID given for Status RPC")
	}

	jid := &jobsv1.JobID{JobID: jobID}
	in := &jobsv1.JobIDRequest{JobIDRequest: jid}

	stream, err := rpcClient.StreamOutput(ctx, in)
	if err != nil {
		log.Printf("Output: open stream error %s", err.Error())
		return err
	}

	for {
		resp, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err // End on errors
		}

		// Show the output of the received stream
		fmt.Println(string(resp.Output))
	}
}
